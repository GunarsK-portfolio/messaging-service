package repository

import (
	"context"
	"fmt"

	"github.com/GunarsK-portfolio/portfolio-common/models"
	"gorm.io/gorm"
)

// Repository defines the interface for messaging worker data operations
type Repository interface {
	// Contact Messages
	GetContactMessageByID(ctx context.Context, id int64) (*models.ContactMessage, error)
	UpdateMessageStatus(ctx context.Context, id int64, status string, lastError *string) error

	// Recipients
	GetActiveRecipients(ctx context.Context) ([]models.Recipient, error)

	// Delivery Attempts
	CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error
}

type repository struct {
	db *gorm.DB
}

// New creates a new repository instance
func New(db *gorm.DB) Repository {
	return &repository{db: db}
}

// GetContactMessageByID retrieves a contact message by ID
func (r *repository) GetContactMessageByID(ctx context.Context, id int64) (*models.ContactMessage, error) {
	var message models.ContactMessage
	err := r.db.WithContext(ctx).First(&message, id).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get contact message %d: %w", id, err)
	}
	return &message, nil
}

// UpdateMessageStatus updates the status of a contact message
func (r *repository) UpdateMessageStatus(ctx context.Context, id int64, status string, lastError *string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if lastError != nil {
		updates["last_error"] = *lastError
	}
	if status == models.MessageStatusSent {
		updates["sent_at"] = r.db.NowFunc()
	}
	if status == models.MessageStatusFailed {
		updates["attempts"] = gorm.Expr("attempts + 1")
	}

	result := r.db.WithContext(ctx).
		Model(&models.ContactMessage{}).
		Where("id = ?", id).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update message status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("message %d not found", id)
	}
	return nil
}

// GetActiveRecipients retrieves all active recipients
func (r *repository) GetActiveRecipients(ctx context.Context) ([]models.Recipient, error) {
	var recipients []models.Recipient
	err := r.db.WithContext(ctx).
		Where("is_active = ?", true).
		Order("name ASC").
		Find(&recipients).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get active recipients: %w", err)
	}
	return recipients, nil
}

// CreateDeliveryAttempt records a delivery attempt
func (r *repository) CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error {
	err := r.db.WithContext(ctx).
		Omit("ID").
		Create(attempt).Error
	if err != nil {
		return fmt.Errorf("failed to create delivery attempt: %w", err)
	}
	return nil
}
