package repository

import (
	"context"
	"fmt"

	"github.com/GunarsK-portfolio/portfolio-common/models"
	commonrepo "github.com/GunarsK-portfolio/portfolio-common/repository"
	"gorm.io/gorm"
)

// Repository defines the interface for messaging worker data operations
type Repository interface {
	// Emails
	GetEmailByID(ctx context.Context, id int64) (*models.Email, error)
	UpdateEmailStatus(ctx context.Context, id int64, status string, lastError *string) error

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

// GetEmailByID retrieves an email by ID
func (r *repository) GetEmailByID(ctx context.Context, id int64) (*models.Email, error) {
	var email models.Email
	err := r.db.WithContext(ctx).First(&email, id).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get email %d: %w", id, err)
	}
	return &email, nil
}

// UpdateEmailStatus delegates to the shared helper in portfolio-common
func (r *repository) UpdateEmailStatus(ctx context.Context, id int64, status string, lastError *string) error {
	return commonrepo.UpdateEmailStatus(r.db, ctx, id, status, lastError)
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
