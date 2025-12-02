package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/GunarsK-portfolio/portfolio-common/logger"
	"github.com/GunarsK-portfolio/portfolio-common/models"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/GunarsK-portfolio/messaging-service/internal/email"
	"github.com/GunarsK-portfolio/messaging-service/internal/repository"
)

// Handler processes contact message events from the queue
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

// Process handles a single message delivery from the queue
func (h *Handler) Process(ctx context.Context, delivery amqp.Delivery) error {
	// Get logger with context (includes correlation_id, request_id if present)
	log := logger.FromContext(ctx, h.logger)

	// Parse the event
	var event models.ContactMessageEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		log.Error("Failed to unmarshal event", "error", err, "body", string(delivery.Body))
		return fmt.Errorf("unmarshal event: %w", err)
	}

	log = log.With("messageId", event.MessageID)
	log.Info("Processing message")

	// Fetch message from database
	message, err := h.repo.GetContactMessageByID(ctx, event.MessageID)
	if err != nil {
		log.Error("Failed to get message", "error", err)
		return fmt.Errorf("get message %d: %w", event.MessageID, err)
	}

	// Skip if already sent
	if message.Status == models.MessageStatusSent {
		log.Info("Message already sent, skipping")
		return nil
	}

	// Get active recipients
	recipients, err := h.repo.GetActiveRecipients(ctx)
	if err != nil {
		log.Error("Failed to get recipients", "error", err)
		return fmt.Errorf("get recipients: %w", err)
	}

	if len(recipients) == 0 {
		log.Warn("No active recipients configured")
		errMsg := "no active recipients configured"
		_ = h.repo.UpdateMessageStatus(ctx, event.MessageID, models.MessageStatusFailed, &errMsg)
		return fmt.Errorf("no active recipients")
	}

	// Send email to each recipient
	subject := fmt.Sprintf("Contact Form: %s", message.Subject)
	body := h.formatEmailBody(message)

	var lastErr error
	successCount := 0

	for _, recipient := range recipients {
		err := h.sendToRecipient(ctx, log, message, recipient, subject, body)
		if err != nil {
			lastErr = err
			log.Error("Failed to send to recipient",
				"recipient", recipient.Email,
				"error", err,
			)
		} else {
			successCount++
		}
	}

	// Update message status based on results
	if successCount > 0 {
		if err := h.repo.UpdateMessageStatus(ctx, event.MessageID, models.MessageStatusSent, nil); err != nil {
			log.Error("Failed to update message status to sent", "error", err)
		}
		log.Info("Message sent successfully",
			"successCount", successCount,
			"totalRecipients", len(recipients),
		)
		return nil
	}

	// All recipients failed
	errMsg := lastErr.Error()
	if err := h.repo.UpdateMessageStatus(ctx, event.MessageID, models.MessageStatusFailed, &errMsg); err != nil {
		log.Error("Failed to update message status to failed", "error", err)
	}

	return fmt.Errorf("all recipients failed: %w", lastErr)
}

// sendToRecipient sends email to a single recipient and records the attempt
func (h *Handler) sendToRecipient(ctx context.Context, log *slog.Logger, message *models.ContactMessage, recipient models.Recipient, subject, body string) error {
	attempt := &models.DeliveryAttempt{
		MessageID:      message.ID,
		RecipientEmail: recipient.Email,
		Status:         models.DeliveryStatusPending,
		AttemptedAt:    time.Now(),
	}

	err := h.emailClient.SendEmail(ctx, recipient.Email, subject, body)
	if err != nil {
		attempt.Status = models.DeliveryStatusFailed
		errMsg := err.Error()
		attempt.ErrorMessage = &errMsg
	} else {
		attempt.Status = models.DeliveryStatusSuccess
	}

	// Record the attempt
	if recordErr := h.repo.CreateDeliveryAttempt(ctx, attempt); recordErr != nil {
		log.Error("Failed to record delivery attempt",
			"recipient", recipient.Email,
			"error", recordErr,
		)
	}

	return err
}

// formatEmailBody creates the email body from the contact message
func (h *Handler) formatEmailBody(message *models.ContactMessage) string {
	return fmt.Sprintf(`New contact form submission:

From: %s <%s>
Subject: %s

Message:
%s

---
Submitted at: %s
Message ID: %d`,
		message.Name,
		message.Email,
		message.Subject,
		message.Message,
		message.CreatedAt.Format(time.RFC1123),
		message.ID,
	)
}
