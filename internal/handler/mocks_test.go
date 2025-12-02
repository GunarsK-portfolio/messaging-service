package handler

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/GunarsK-portfolio/messaging-service/internal/email"
	"github.com/GunarsK-portfolio/messaging-service/internal/repository"
	"github.com/GunarsK-portfolio/portfolio-common/models"
)

// =============================================================================
// Mock Repository
// =============================================================================

type mockRepository struct {
	getContactMessageByIDFunc func(ctx context.Context, id int64) (*models.ContactMessage, error)
	updateMessageStatusFunc   func(ctx context.Context, id int64, status string, lastError *string) error
	getActiveRecipientsFunc   func(ctx context.Context) ([]models.Recipient, error)
	createDeliveryAttemptFunc func(ctx context.Context, attempt *models.DeliveryAttempt) error
}

func (m *mockRepository) GetContactMessageByID(ctx context.Context, id int64) (*models.ContactMessage, error) {
	if m.getContactMessageByIDFunc != nil {
		return m.getContactMessageByIDFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockRepository) UpdateMessageStatus(ctx context.Context, id int64, status string, lastError *string) error {
	if m.updateMessageStatusFunc != nil {
		return m.updateMessageStatusFunc(ctx, id, status, lastError)
	}
	return nil
}

func (m *mockRepository) GetActiveRecipients(ctx context.Context) ([]models.Recipient, error) {
	if m.getActiveRecipientsFunc != nil {
		return m.getActiveRecipientsFunc(ctx)
	}
	return nil, nil
}

func (m *mockRepository) CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error {
	if m.createDeliveryAttemptFunc != nil {
		return m.createDeliveryAttemptFunc(ctx, attempt)
	}
	return nil
}

// Verify mock implements Repository interface
var _ repository.Repository = (*mockRepository)(nil)

// =============================================================================
// Mock Email Client
// =============================================================================

type mockEmailClient struct {
	sendEmailFunc func(ctx context.Context, to, subject, body string) error
}

func (m *mockEmailClient) SendEmail(ctx context.Context, to, subject, body string) error {
	if m.sendEmailFunc != nil {
		return m.sendEmailFunc(ctx, to, subject, body)
	}
	return nil
}

// Verify mock implements Client interface
var _ email.Client = (*mockEmailClient)(nil)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func createTestContactMessage() *models.ContactMessage {
	return &models.ContactMessage{
		ID:        1,
		Name:      "John Doe",
		Email:     "john@example.com",
		Subject:   "Test Subject",
		Message:   "Test message content",
		Status:    models.MessageStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func createTestRecipient() models.Recipient {
	return models.Recipient{
		ID:        1,
		Email:     "admin@example.com",
		Name:      "Admin User",
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func createTestRecipients() []models.Recipient {
	return []models.Recipient{
		{
			ID:        1,
			Email:     "admin@example.com",
			Name:      "Admin User",
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:        2,
			Email:     "support@example.com",
			Name:      "Support Team",
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
}

// Context key for propagation tests
type ctxKey struct{}
