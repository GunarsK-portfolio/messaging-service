package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/GunarsK-portfolio/portfolio-common/models"
	amqp "github.com/rabbitmq/amqp091-go"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNew_ReturnsHandler(t *testing.T) {
	mockRepo := &mockRepository{}
	mockEmail := &mockEmailClient{}
	logger := createTestLogger()

	handler := New(mockRepo, mockEmail, logger)

	if handler == nil {
		t.Fatal("expected handler to not be nil")
	}
	if handler.repo == nil {
		t.Error("expected repo to be set")
	}
	if handler.emailClient == nil {
		t.Error("expected emailClient to be set")
	}
	if handler.logger == nil {
		t.Error("expected logger to be set")
	}
}

// =============================================================================
// Process Tests - Contact Form Success
// =============================================================================

func TestProcess_ContactFormSuccess(t *testing.T) {
	email := createTestContactFormEmail()
	recipients := createTestRecipients()
	var updatedStatus string
	var emailsSent []string
	var deliveryAttempts []*models.DeliveryAttempt
	var mu sync.Mutex

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, id int64) (*models.Email, error) {
			if id != 1 {
				t.Errorf("expected id 1, got %d", id)
			}
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return recipients, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, attempt *models.DeliveryAttempt) error {
			mu.Lock()
			deliveryAttempts = append(deliveryAttempts, attempt)
			mu.Unlock()
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, to, _, _ string) error {
			mu.Lock()
			emailsSent = append(emailsSent, to)
			mu.Unlock()
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if updatedStatus != models.EmailStatusSent {
		t.Errorf("expected status %q, got %q", models.EmailStatusSent, updatedStatus)
	}
	if len(emailsSent) != len(recipients) {
		t.Errorf("expected %d emails sent, got %d", len(recipients), len(emailsSent))
	}
	if len(deliveryAttempts) != len(recipients) {
		t.Errorf("expected %d delivery attempts, got %d", len(recipients), len(deliveryAttempts))
	}
	for _, attempt := range deliveryAttempts {
		if attempt.Status != models.DeliveryStatusSuccess {
			t.Errorf("expected delivery status %q, got %q", models.DeliveryStatusSuccess, attempt.Status)
		}
	}
}

func TestProcess_ContactFormSingleRecipient(t *testing.T) {
	email := createTestContactFormEmail()
	recipient := createTestRecipient()
	var emailSentTo string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, to, _, _ string) error {
			emailSentTo = to
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if emailSentTo != recipient.Email {
		t.Errorf("expected email sent to %q, got %q", recipient.Email, emailSentTo)
	}
}

// =============================================================================
// Process Tests - Direct Email Success
// =============================================================================

func TestProcess_DirectEmailSuccess(t *testing.T) {
	email := createTestDirectEmail()
	var sentTo, sentSubject, sentBody string
	var updatedStatus string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, to, subject, body string) error {
			sentTo = to
			sentSubject = subject
			sentBody = body
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 2}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if sentTo != *email.RecipientEmail {
		t.Errorf("expected sent to %q, got %q", *email.RecipientEmail, sentTo)
	}
	if sentSubject != email.Subject {
		t.Errorf("expected subject %q, got %q", email.Subject, sentSubject)
	}
	if sentBody != email.Message {
		t.Errorf("expected body %q, got %q", email.Message, sentBody)
	}
	if updatedStatus != models.EmailStatusSent {
		t.Errorf("expected status %q, got %q", models.EmailStatusSent, updatedStatus)
	}
}

func TestProcess_DirectEmailNoRecipient(t *testing.T) {
	email := createTestDirectEmail()
	email.RecipientEmail = nil
	var updatedStatus string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 2}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when recipient_email is nil")
	}
	if updatedStatus != models.EmailStatusFailed {
		t.Errorf("expected status %q, got %q", models.EmailStatusFailed, updatedStatus)
	}
}

func TestProcess_DirectEmailSendFailure(t *testing.T) {
	email := createTestDirectEmail()
	var updatedStatus string
	var lastError *string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, err *string) error {
			updatedStatus = status
			lastError = err
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return errors.New("SES error")
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 2}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when send fails")
	}
	if updatedStatus != models.EmailStatusFailed {
		t.Errorf("expected status %q, got %q", models.EmailStatusFailed, updatedStatus)
	}
	if lastError == nil {
		t.Error("expected error message to be set")
	}
}

// =============================================================================
// Process Tests - Skip Already Sent
// =============================================================================

func TestProcess_SkipsAlreadySentEmail(t *testing.T) {
	email := createTestContactFormEmail()
	email.Status = models.EmailStatusSent
	emailCalled := false
	updateCalled := false

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
			updateCalled = true
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			emailCalled = true
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if emailCalled {
		t.Error("expected email NOT to be sent for already sent email")
	}
	if updateCalled {
		t.Error("expected status NOT to be updated for already sent email")
	}
}

// =============================================================================
// Process Tests - Error Cases
// =============================================================================

func TestProcess_InvalidJSON(t *testing.T) {
	mockRepo := &mockRepository{}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	delivery := amqp.Delivery{Body: []byte(`{invalid json}`)}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail for invalid JSON")
	}
}

func TestProcess_EmailNotFound(t *testing.T) {
	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return nil, errors.New("email not found")
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 999}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when email not found")
	}
}

func TestProcess_NoActiveRecipients(t *testing.T) {
	email := createTestContactFormEmail()
	var updatedStatus string
	var lastError *string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, err *string) error {
			updatedStatus = status
			lastError = err
			return nil
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when no active recipients")
	}
	if updatedStatus != models.EmailStatusFailed {
		t.Errorf("expected status %q, got %q", models.EmailStatusFailed, updatedStatus)
	}
	if lastError == nil || *lastError != "no active recipients configured" {
		t.Error("expected error message about no active recipients")
	}
}

func TestProcess_GetRecipientsError(t *testing.T) {
	email := createTestContactFormEmail()

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return nil, errors.New("database error")
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when getting recipients fails")
	}
}

func TestProcess_AllEmailsFail(t *testing.T) {
	email := createTestContactFormEmail()
	recipients := createTestRecipients()
	var updatedStatus string
	var lastError *string
	var deliveryAttempts []*models.DeliveryAttempt
	var mu sync.Mutex

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return recipients, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, err *string) error {
			updatedStatus = status
			lastError = err
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, attempt *models.DeliveryAttempt) error {
			mu.Lock()
			deliveryAttempts = append(deliveryAttempts, attempt)
			mu.Unlock()
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return errors.New("SES error")
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when all emails fail")
	}
	if updatedStatus != models.EmailStatusFailed {
		t.Errorf("expected status %q, got %q", models.EmailStatusFailed, updatedStatus)
	}
	if lastError == nil {
		t.Error("expected error message to be set")
	}
	for _, attempt := range deliveryAttempts {
		if attempt.Status != models.DeliveryStatusFailed {
			t.Errorf("expected delivery status %q, got %q", models.DeliveryStatusFailed, attempt.Status)
		}
		if attempt.ErrorMessage == nil {
			t.Error("expected error message on failed delivery attempt")
		}
	}
}

func TestProcess_PartialEmailFailure(t *testing.T) {
	email := createTestContactFormEmail()
	recipients := createTestRecipients()
	var updatedStatus string
	var emailAttempts int
	var mu sync.Mutex

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return recipients, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			mu.Lock()
			emailAttempts++
			attempt := emailAttempts
			mu.Unlock()
			if attempt == 1 {
				return nil
			}
			return errors.New("SES error")
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v, expected success with partial failure", err)
	}
	if updatedStatus != models.EmailStatusSent {
		t.Errorf("expected status %q, got %q", models.EmailStatusSent, updatedStatus)
	}
}

// =============================================================================
// Process Tests - Delivery Attempt Recording
// =============================================================================

func TestProcess_RecordsDeliveryAttemptOnSuccess(t *testing.T) {
	email := createTestContactFormEmail()
	recipient := createTestRecipient()
	var recordedAttempt *models.DeliveryAttempt

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, attempt *models.DeliveryAttempt) error {
			recordedAttempt = attempt
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if recordedAttempt == nil {
		t.Fatal("expected delivery attempt to be recorded")
	}
	if recordedAttempt.MessageID != email.ID {
		t.Errorf("expected MessageID %d, got %d", email.ID, recordedAttempt.MessageID)
	}
	if recordedAttempt.RecipientEmail != recipient.Email {
		t.Errorf("expected RecipientEmail %q, got %q", recipient.Email, recordedAttempt.RecipientEmail)
	}
	if recordedAttempt.Status != models.DeliveryStatusSuccess {
		t.Errorf("expected Status %q, got %q", models.DeliveryStatusSuccess, recordedAttempt.Status)
	}
	if recordedAttempt.ErrorMessage != nil {
		t.Error("expected no error message on success")
	}
}

func TestProcess_RecordsDeliveryAttemptOnFailure(t *testing.T) {
	email := createTestContactFormEmail()
	recipient := createTestRecipient()
	var recordedAttempt *models.DeliveryAttempt

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, attempt *models.DeliveryAttempt) error {
			recordedAttempt = attempt
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return errors.New("SES connection failed")
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	_ = handler.Process(context.Background(), delivery)

	if recordedAttempt == nil {
		t.Fatal("expected delivery attempt to be recorded")
	}
	if recordedAttempt.Status != models.DeliveryStatusFailed {
		t.Errorf("expected Status %q, got %q", models.DeliveryStatusFailed, recordedAttempt.Status)
	}
	if recordedAttempt.ErrorMessage == nil {
		t.Error("expected error message on failure")
	}
	if *recordedAttempt.ErrorMessage != "SES connection failed" {
		t.Errorf("expected error message %q, got %q", "SES connection failed", *recordedAttempt.ErrorMessage)
	}
}

func TestProcess_ContinuesEvenWhenDeliveryAttemptRecordFails(t *testing.T) {
	email := createTestContactFormEmail()
	recipient := createTestRecipient()
	var updatedStatus string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return errors.New("database error")
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v, expected success", err)
	}
	if updatedStatus != models.EmailStatusSent {
		t.Errorf("expected status %q, got %q", models.EmailStatusSent, updatedStatus)
	}
}

// =============================================================================
// Process Tests - Email Content
// =============================================================================

func TestProcess_ContactFormEmailContentFormat(t *testing.T) {
	email := createTestContactFormEmail()
	email.Name = strPtr("Test User")
	email.SenderEmail = strPtr("testuser@example.com")
	email.Subject = "Important Subject"
	email.Message = "This is the message body"
	recipient := createTestRecipient()
	var sentSubject, sentBody string

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(_ context.Context, _ int64) (*models.Email, error) {
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, subject, body string) error {
			sentSubject = subject
			sentBody = body
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	expectedSubject := "Contact Form: Important Subject"
	if sentSubject != expectedSubject {
		t.Errorf("expected subject %q, got %q", expectedSubject, sentSubject)
	}

	if sentBody == "" {
		t.Error("expected body to be non-empty")
	}
	if !strings.Contains(sentBody, "Test User") {
		t.Error("expected body to contain sender name")
	}
	if !strings.Contains(sentBody, "testuser@example.com") {
		t.Error("expected body to contain sender email")
	}
	if !strings.Contains(sentBody, "This is the message body") {
		t.Error("expected body to contain message content")
	}
}

// =============================================================================
// Context Propagation Tests
// =============================================================================

func TestProcess_ContextPropagation(t *testing.T) {
	email := createTestContactFormEmail()
	recipient := createTestRecipient()
	var capturedCtx context.Context

	mockRepo := &mockRepository{
		getEmailByIDFunc: func(ctx context.Context, _ int64) (*models.Email, error) {
			capturedCtx = ctx
			return email, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateEmailStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	ctx := context.WithValue(context.Background(), ctxKey{}, "test-marker")

	event := models.EmailEvent{EmailID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(ctx, delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if capturedCtx == nil {
		t.Error("expected context to be propagated to repository")
	}
	if capturedCtx.Value(ctxKey{}) != "test-marker" {
		t.Error("context sentinel value was not propagated to repository")
	}
}

// =============================================================================
// formatContactFormBody Tests
// =============================================================================

func TestFormatContactFormBody_ContainsAllFields(t *testing.T) {
	handler := New(&mockRepository{}, &mockEmailClient{}, createTestLogger())
	email := createTestContactFormEmail()
	email.Name = strPtr("Jane Smith")
	email.SenderEmail = strPtr("jane@test.com")
	email.Subject = "Test Inquiry"
	email.Message = "Hello, this is a test message."
	email.ID = 42

	body := handler.formatContactFormBody(email)

	checks := []struct {
		name     string
		expected string
	}{
		{"name", "Jane Smith"},
		{"email", "jane@test.com"},
		{"subject", "Test Inquiry"},
		{"message", "Hello, this is a test message."},
		{"email ID", "42"},
	}

	for _, check := range checks {
		if !strings.Contains(body, check.expected) {
			t.Errorf("expected body to contain %s (%q)", check.name, check.expected)
		}
	}
}

// =============================================================================
// stripHTML Tests
// =============================================================================

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain text", "hello world", "hello world"},
		{"simple tags", "<p>hello</p>", "hello"},
		{"nested tags", "<div><p>hello</p></div>", "hello"},
		{"self-closing", "hello<br/>world", "helloworld"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.expected {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
