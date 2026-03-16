package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/GunarsK-portfolio/portfolio-common/logger"
	"github.com/GunarsK-portfolio/portfolio-common/models"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/GunarsK-portfolio/messaging-service/internal/email"
	"github.com/GunarsK-portfolio/messaging-service/internal/repository"
)

// maxConcurrentEmails limits parallel email sending to avoid overwhelming SES
const maxConcurrentEmails = 5

// Handler processes email events from the queue
type Handler struct {
	repo        repository.Repository
	emailClient email.Client
	logger      *slog.Logger
}

// New creates a new message handler
func New(repo repository.Repository, emailClient email.Client, logger *slog.Logger) *Handler {
	return &Handler{
		repo:        repo,
		emailClient: emailClient,
		logger:      logger,
	}
}

// Process handles a single email delivery from the queue
func (h *Handler) Process(ctx context.Context, delivery amqp.Delivery) error {
	log := logger.FromContext(ctx, h.logger)

	var event models.EmailEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		log.Error("Failed to unmarshal event", "error", err, "bodyLength", len(delivery.Body))
		return fmt.Errorf("unmarshal event: %w", err)
	}

	log = log.With("emailId", event.EmailID)
	log.Info("Processing email")

	msg, err := h.repo.GetEmailByID(ctx, event.EmailID)
	if err != nil {
		log.Error("Failed to get email", "error", err)
		return fmt.Errorf("get email %d: %w", event.EmailID, err)
	}

	if msg.Status == models.EmailStatusSent {
		log.Info("Email already sent, skipping")
		return nil
	}

	// Route based on email type
	if msg.Type == models.EmailTypeContactForm {
		return h.processContactForm(ctx, log, msg)
	}
	return h.processDirectEmail(ctx, log, msg)
}

// processContactForm sends to all active recipients from the recipients table
func (h *Handler) processContactForm(ctx context.Context, log *slog.Logger, msg *models.Email) error {
	recipients, err := h.repo.GetActiveRecipients(ctx)
	if err != nil {
		log.Error("Failed to get recipients", "error", err)
		return fmt.Errorf("get recipients: %w", err)
	}

	if len(recipients) == 0 {
		log.Warn("No active recipients configured")
		errMsg := "no active recipients configured"
		if err := h.repo.UpdateEmailStatus(ctx, msg.ID, models.EmailStatusFailed, &errMsg); err != nil {
			log.Error("Failed to update email status to failed", "error", err)
		}
		return fmt.Errorf("no active recipients")
	}

	subject := fmt.Sprintf("Contact Form: %s", msg.Subject)
	body := h.formatContactFormBody(msg)

	type result struct {
		recipient string
		err       error
	}

	var (
		wg          sync.WaitGroup
		resultsChan = make(chan result, len(recipients))
		semaphore   = make(chan struct{}, maxConcurrentEmails)
	)

	for _, recipient := range recipients {
		wg.Add(1)
		go func(r models.Recipient) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			sendErr := h.sendAndRecord(ctx, log, msg, r.Email, subject, body)
			resultsChan <- result{recipient: r.Email, err: sendErr}
		}(recipient)
	}

	wg.Wait()
	close(resultsChan)

	var lastErr error
	successCount := 0

	for res := range resultsChan {
		if res.err != nil {
			lastErr = res.err
			log.Error("Failed to send to recipient", "recipient", res.recipient, "error", res.err)
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		if err := h.repo.UpdateEmailStatus(ctx, msg.ID, models.EmailStatusSent, nil); err != nil {
			log.Error("Failed to update email status to sent", "error", err)
		}
		log.Info("Email sent successfully", "successCount", successCount, "totalRecipients", len(recipients))
		return nil
	}

	errMsg := lastErr.Error()
	if err := h.repo.UpdateEmailStatus(ctx, msg.ID, models.EmailStatusFailed, &errMsg); err != nil {
		log.Error("Failed to update email status to failed", "error", err)
	}
	return fmt.Errorf("all recipients failed: %w", lastErr)
}

// processDirectEmail sends to the recipient_email on the email record (auth emails, etc.)
func (h *Handler) processDirectEmail(ctx context.Context, log *slog.Logger, msg *models.Email) error {
	if msg.RecipientEmail == nil || *msg.RecipientEmail == "" {
		errMsg := "no recipient_email set for direct email"
		if err := h.repo.UpdateEmailStatus(ctx, msg.ID, models.EmailStatusFailed, &errMsg); err != nil {
			log.Error("Failed to update email status to failed", "error", err)
		}
		return fmt.Errorf("no recipient_email for email %d", msg.ID)
	}

	to := *msg.RecipientEmail
	err := h.sendAndRecord(ctx, log, msg, to, msg.Subject, msg.Message)
	if err != nil {
		errMsg := err.Error()
		if updateErr := h.repo.UpdateEmailStatus(ctx, msg.ID, models.EmailStatusFailed, &errMsg); updateErr != nil {
			log.Error("Failed to update email status to failed", "error", updateErr)
		}
		return fmt.Errorf("send to %s failed: %w", to, err)
	}

	if err := h.repo.UpdateEmailStatus(ctx, msg.ID, models.EmailStatusSent, nil); err != nil {
		log.Error("Failed to update email status to sent", "error", err)
	}
	log.Info("Email sent successfully", "recipient", to)
	return nil
}

// sendAndRecord sends an email and records the delivery attempt
func (h *Handler) sendAndRecord(ctx context.Context, log *slog.Logger, msg *models.Email, to, subject, body string) error {
	attempt := &models.DeliveryAttempt{
		MessageID:      msg.ID,
		RecipientEmail: to,
		Status:         models.DeliveryStatusPending,
		AttemptedAt:    time.Now().UTC(),
	}

	err := h.emailClient.SendEmail(ctx, to, subject, body)
	if err != nil {
		attempt.Status = models.DeliveryStatusFailed
		errMsg := err.Error()
		attempt.ErrorMessage = &errMsg
	} else {
		attempt.Status = models.DeliveryStatusSuccess
	}

	if recordErr := h.repo.CreateDeliveryAttempt(ctx, attempt); recordErr != nil {
		log.Error("Failed to record delivery attempt", "recipient", to, "error", recordErr)
	}

	return err
}

var htmlTagRegex = regexp.MustCompile(`<[^>]*>`)

// stripHTML removes HTML tags for plain-text fallback
func stripHTML(html string) string {
	return htmlTagRegex.ReplaceAllString(html, "")
}

// formatContactFormBody creates the email body from a contact form submission
func (h *Handler) formatContactFormBody(msg *models.Email) string {
	name := ""
	if msg.Name != nil {
		name = *msg.Name
	}
	emailAddr := ""
	if msg.SenderEmail != nil {
		emailAddr = *msg.SenderEmail
	}
	return fmt.Sprintf(`New contact form submission:

From: %s <%s>
Subject: %s

Message:
%s

---
Submitted at: %s
Email ID: %d`,
		name,
		emailAddr,
		msg.Subject,
		msg.Message,
		msg.CreatedAt.Format(time.RFC1123),
		msg.ID,
	)
}
