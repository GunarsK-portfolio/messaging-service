package handler

import (
	"context"
	"encoding/json"
	"errors"
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
// Process Tests - Success Cases
// =============================================================================

func TestProcess_Success(t *testing.T) {
	message := createTestContactMessage()
	recipients := createTestRecipients()
	var updatedStatus string
	var emailsSent []string
	var deliveryAttempts []*models.DeliveryAttempt

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, id int64) (*models.ContactMessage, error) {
			if id != 1 {
				t.Errorf("expected id 1, got %d", id)
			}
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return recipients, nil
		},
		updateMessageStatusFunc: func(_ context.Context, id int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, attempt *models.DeliveryAttempt) error {
			deliveryAttempts = append(deliveryAttempts, attempt)
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, to, _, _ string) error {
			emailsSent = append(emailsSent, to)
			return nil
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if updatedStatus != models.MessageStatusSent {
		t.Errorf("expected status %q, got %q", models.MessageStatusSent, updatedStatus)
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

func TestProcess_SingleRecipient(t *testing.T) {
	message := createTestContactMessage()
	recipient := createTestRecipient()
	var emailSentTo string

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
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

	event := models.ContactMessageEvent{MessageID: 1}
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
// Process Tests - Skip Already Sent
// =============================================================================

func TestProcess_SkipsAlreadySentMessage(t *testing.T) {
	message := createTestContactMessage()
	message.Status = models.MessageStatusSent
	emailCalled := false
	updateCalled := false

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
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

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if emailCalled {
		t.Error("expected email NOT to be sent for already sent message")
	}
	if updateCalled {
		t.Error("expected status NOT to be updated for already sent message")
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

func TestProcess_MessageNotFound(t *testing.T) {
	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return nil, errors.New("message not found")
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.ContactMessageEvent{MessageID: 999}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when message not found")
	}
}

func TestProcess_NoActiveRecipients(t *testing.T) {
	message := createTestContactMessage()
	var updatedStatus string
	var lastError *string

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, status string, err *string) error {
			updatedStatus = status
			lastError = err
			return nil
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when no active recipients")
	}
	if updatedStatus != models.MessageStatusFailed {
		t.Errorf("expected status %q, got %q", models.MessageStatusFailed, updatedStatus)
	}
	if lastError == nil || *lastError != "no active recipients configured" {
		t.Error("expected error message about no active recipients")
	}
}

func TestProcess_GetRecipientsError(t *testing.T) {
	message := createTestContactMessage()

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return nil, errors.New("database error")
		},
	}
	mockEmail := &mockEmailClient{}
	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when getting recipients fails")
	}
}

func TestProcess_AllEmailsFail(t *testing.T) {
	message := createTestContactMessage()
	recipients := createTestRecipients()
	var updatedStatus string
	var lastError *string
	var deliveryAttempts []*models.DeliveryAttempt

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return recipients, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, status string, err *string) error {
			updatedStatus = status
			lastError = err
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, attempt *models.DeliveryAttempt) error {
			deliveryAttempts = append(deliveryAttempts, attempt)
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, _, _, _ string) error {
			return errors.New("SES error")
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err == nil {
		t.Error("Process() should fail when all emails fail")
	}
	if updatedStatus != models.MessageStatusFailed {
		t.Errorf("expected status %q, got %q", models.MessageStatusFailed, updatedStatus)
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
	message := createTestContactMessage()
	recipients := createTestRecipients()
	var updatedStatus string
	emailAttempts := 0

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return recipients, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
			updatedStatus = status
			return nil
		},
		createDeliveryAttemptFunc: func(_ context.Context, _ *models.DeliveryAttempt) error {
			return nil
		},
	}
	mockEmail := &mockEmailClient{
		sendEmailFunc: func(_ context.Context, to, _, _ string) error {
			emailAttempts++
			// First email succeeds, second fails
			if emailAttempts == 1 {
				return nil
			}
			return errors.New("SES error")
		},
	}

	handler := New(mockRepo, mockEmail, createTestLogger())

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	// Should succeed if at least one email was sent
	if err != nil {
		t.Fatalf("Process() error = %v, expected success with partial failure", err)
	}
	if updatedStatus != models.MessageStatusSent {
		t.Errorf("expected status %q, got %q", models.MessageStatusSent, updatedStatus)
	}
}

// =============================================================================
// Process Tests - Delivery Attempt Recording
// =============================================================================

func TestProcess_RecordsDeliveryAttemptOnSuccess(t *testing.T) {
	message := createTestContactMessage()
	recipient := createTestRecipient()
	var recordedAttempt *models.DeliveryAttempt

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
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

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if recordedAttempt == nil {
		t.Fatal("expected delivery attempt to be recorded")
	}
	if recordedAttempt.MessageID != message.ID {
		t.Errorf("expected MessageID %d, got %d", message.ID, recordedAttempt.MessageID)
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
	message := createTestContactMessage()
	recipient := createTestRecipient()
	var recordedAttempt *models.DeliveryAttempt

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
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

	event := models.ContactMessageEvent{MessageID: 1}
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
	message := createTestContactMessage()
	recipient := createTestRecipient()
	var updatedStatus string

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, status string, _ *string) error {
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

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	err := handler.Process(context.Background(), delivery)

	// Should still succeed even if recording delivery attempt fails
	if err != nil {
		t.Fatalf("Process() error = %v, expected success", err)
	}
	if updatedStatus != models.MessageStatusSent {
		t.Errorf("expected status %q, got %q", models.MessageStatusSent, updatedStatus)
	}
}

// =============================================================================
// Process Tests - Email Content
// =============================================================================

func TestProcess_EmailContentFormat(t *testing.T) {
	message := createTestContactMessage()
	message.Name = "Test User"
	message.Email = "testuser@example.com"
	message.Subject = "Important Subject"
	message.Message = "This is the message body"
	recipient := createTestRecipient()
	var sentSubject, sentBody string

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(_ context.Context, _ int64) (*models.ContactMessage, error) {
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
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

	event := models.ContactMessageEvent{MessageID: 1}
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

	// Verify body contains key information
	if sentBody == "" {
		t.Error("expected body to be non-empty")
	}
	if !contains(sentBody, "Test User") {
		t.Error("expected body to contain sender name")
	}
	if !contains(sentBody, "testuser@example.com") {
		t.Error("expected body to contain sender email")
	}
	if !contains(sentBody, "This is the message body") {
		t.Error("expected body to contain message content")
	}
}

// =============================================================================
// Context Propagation Tests
// =============================================================================

func TestProcess_ContextPropagation(t *testing.T) {
	message := createTestContactMessage()
	recipient := createTestRecipient()
	var capturedCtx context.Context

	mockRepo := &mockRepository{
		getContactMessageByIDFunc: func(ctx context.Context, _ int64) (*models.ContactMessage, error) {
			capturedCtx = ctx
			return message, nil
		},
		getActiveRecipientsFunc: func(_ context.Context) ([]models.Recipient, error) {
			return []models.Recipient{recipient}, nil
		},
		updateMessageStatusFunc: func(_ context.Context, _ int64, _ string, _ *string) error {
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

	// Create context with sentinel value
	ctx := context.WithValue(context.Background(), ctxKey{}, "test-marker")

	event := models.ContactMessageEvent{MessageID: 1}
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
// formatEmailBody Tests
// =============================================================================

func TestFormatEmailBody_ContainsAllFields(t *testing.T) {
	handler := New(&mockRepository{}, &mockEmailClient{}, createTestLogger())
	message := createTestContactMessage()
	message.Name = "Jane Smith"
	message.Email = "jane@test.com"
	message.Subject = "Test Inquiry"
	message.Message = "Hello, this is a test message."
	message.ID = 42

	body := handler.formatEmailBody(message)

	checks := []struct {
		name     string
		expected string
	}{
		{"name", "Jane Smith"},
		{"email", "jane@test.com"},
		{"subject", "Test Inquiry"},
		{"message", "Hello, this is a test message."},
		{"message ID", "42"},
	}

	for _, check := range checks {
		if !contains(body, check.expected) {
			t.Errorf("expected body to contain %s (%q)", check.name, check.expected)
		}
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
