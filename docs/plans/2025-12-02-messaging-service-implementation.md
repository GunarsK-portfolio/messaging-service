# messaging-service Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a background worker that consumes contact form messages from RabbitMQ and sends email notifications via SES.

**Architecture:** Consumer in portfolio-common (reusable), messaging-service handles email logic. ACK+PublishToRetry for retriable failures, NACK after max retries. Idempotency via delivery_attempts table.

**Tech Stack:** Go 1.25, RabbitMQ (amqp091-go), AWS SDK v2 (SES), GORM, portfolio-common

---

## Task 1: Initialize messaging-service Repository

**Files:**
- Create: `messaging-service/go.mod`
- Create: `messaging-service/go.sum`
- Create: `messaging-service/.gitignore`
- Create: `messaging-service/README.md`

**Step 1: Initialize git repository**

```bash
cd d:/Darbs/git/portfolio/messaging-service
git init
git remote add origin https://github.com/GunarsK-portfolio/messaging-service.git
```

**Step 2: Create go.mod**

```bash
go mod init github.com/GunarsK-portfolio/messaging-service
```

**Step 3: Create .gitignore**

```gitignore
# Binaries
bin/
*.exe
*.exe~
*.dll
*.so
*.dylib

# Test coverage
coverage.out
coverage.html

# Security reports
gosec-report.json

# IDE
.idea/
.vscode/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Environment
.env
.env.local
```

**Step 4: Create README.md**

```markdown
# messaging-service

Background worker that consumes contact form messages from RabbitMQ and sends email notifications via SES.

## Overview

- Consumes `ContactMessageEvent` from `contact_messages` queue
- Fetches message details and active recipients from PostgreSQL
- Sends emails via SES (LocalStack locally, AWS SES in production)
- Tracks delivery attempts for idempotency
- Supports retry with exponential backoff (1m, 5m, 30m, 2h, 12h)

## Development

```bash
# Install dependencies
go mod download

# Run locally
go run cmd/worker/main.go

# Run tests
task test

# Run all CI checks
task ci:all
```

## Environment Variables

See `docs/plans/2025-12-02-messaging-service-design.md` for full configuration.
```

**Step 5: Commit**

```bash
git add .
git commit -m "Initialize messaging-service repository"
```

---

## Task 2: Add RabbitMQ Consumer to portfolio-common

**Files:**
- Create: `portfolio-common/queue/consumer.go`
- Create: `portfolio-common/queue/consumer_test.go`

**Step 1: Write consumer test**

Create `portfolio-common/queue/consumer_test.go`:

```go
package queue

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRetryCount(t *testing.T) {
	tests := []struct {
		name     string
		headers  amqp.Table
		expected int
	}{
		{
			name:     "no header returns 0",
			headers:  nil,
			expected: 0,
		},
		{
			name:     "empty headers returns 0",
			headers:  amqp.Table{},
			expected: 0,
		},
		{
			name:     "header with int32 value",
			headers:  amqp.Table{RetryCountHeader: int32(3)},
			expected: 3,
		},
		{
			name:     "header with int64 value",
			headers:  amqp.Table{RetryCountHeader: int64(5)},
			expected: 5,
		},
		{
			name:     "header with int value",
			headers:  amqp.Table{RetryCountHeader: int(2)},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delivery := amqp.Delivery{Headers: tt.headers}
			result := GetRetryCount(delivery)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConsumerConfig(t *testing.T) {
	cfg := ConsumerConfig{
		PrefetchCount: 5,
		ConsumerTag:   "test-consumer",
	}

	assert.Equal(t, 5, cfg.PrefetchCount)
	assert.Equal(t, "test-consumer", cfg.ConsumerTag)
}
```

**Step 2: Run test to verify it fails**

```bash
cd d:/Darbs/git/portfolio/portfolio-common
go test -v ./queue/... -run TestGetRetryCount
```

Expected: FAIL - functions not defined

**Step 3: Write consumer implementation**

Create `portfolio-common/queue/consumer.go`:

```go
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/GunarsK-portfolio/portfolio-common/config"
	amqp "github.com/rabbitmq/amqp091-go"
)

// RetryCountHeader is the AMQP header key for tracking retry attempts
const RetryCountHeader = "x-retry-count"

// Consumer errors
var (
	ErrConsumerClosed     = errors.New("consumer is closed")
	ErrConsumeSetupFailed = errors.New("failed to setup consumer")
)

// MessageHandler processes a single message delivery.
// Return nil to ACK the message, return error to trigger retry logic.
type MessageHandler func(ctx context.Context, delivery amqp.Delivery) error

// Consumer defines the interface for message queue consuming
type Consumer interface {
	// Consume starts consuming messages and blocks until context is cancelled
	Consume(ctx context.Context, handler MessageHandler) error
	// Close stops consuming and closes connections
	Close() error
}

// ConsumerConfig holds consumer-specific configuration
type ConsumerConfig struct {
	PrefetchCount int    // Number of messages to prefetch (QoS)
	ConsumerTag   string // Unique identifier for this consumer
}

// DefaultConsumerConfig returns sensible defaults
func DefaultConsumerConfig(serviceName string) ConsumerConfig {
	return ConsumerConfig{
		PrefetchCount: 1,  // Process one at a time for reliable retry
		ConsumerTag:   serviceName,
	}
}

// RabbitMQConsumer implements Consumer for RabbitMQ
type RabbitMQConsumer struct {
	mu        sync.Mutex
	closed    bool
	conn      *amqp.Connection
	channel   *amqp.Channel
	publisher *RabbitMQPublisher
	queueName string
	config    ConsumerConfig
	logger    *slog.Logger
}

// NewRabbitMQConsumer creates a new consumer that shares queue infrastructure with the publisher.
// The publisher must be created first as it declares all queues.
func NewRabbitMQConsumer(
	cfg config.RabbitMQConfig,
	publisher *RabbitMQPublisher,
	consumerCfg ConsumerConfig,
	logger *slog.Logger,
) (*RabbitMQConsumer, error) {
	conn, err := amqp.Dial(cfg.URL())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrChannelFailed, err)
	}

	// Set QoS for fair dispatch
	if err := ch.Qos(consumerCfg.PrefetchCount, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("%w: set qos: %v", ErrConsumeSetupFailed, err)
	}

	return &RabbitMQConsumer{
		conn:      conn,
		channel:   ch,
		publisher: publisher,
		queueName: cfg.Queue,
		config:    consumerCfg,
		logger:    logger,
	}, nil
}

// Consume starts consuming messages from the queue.
// Blocks until context is cancelled or an error occurs.
// The handler is called for each message; return nil to ACK, error to handle retry.
func (c *RabbitMQConsumer) Consume(ctx context.Context, handler MessageHandler) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrConsumerClosed
	}
	c.mu.Unlock()

	deliveries, err := c.channel.Consume(
		c.queueName,
		c.config.ConsumerTag,
		false, // auto-ack disabled for manual control
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("%w: consume: %v", ErrConsumeSetupFailed, err)
	}

	c.logger.Info("Consumer started", "queue", c.queueName, "tag", c.config.ConsumerTag)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Consumer stopping", "reason", ctx.Err())
			return ctx.Err()

		case delivery, ok := <-deliveries:
			if !ok {
				c.logger.Warn("Delivery channel closed")
				return errors.New("delivery channel closed")
			}

			c.processDelivery(ctx, delivery, handler)
		}
	}
}

// processDelivery handles a single message with retry logic
func (c *RabbitMQConsumer) processDelivery(ctx context.Context, delivery amqp.Delivery, handler MessageHandler) {
	retryCount := GetRetryCount(delivery)

	c.logger.Debug("Processing message",
		"messageId", delivery.MessageId,
		"correlationId", delivery.CorrelationId,
		"retryCount", retryCount,
	)

	err := handler(ctx, delivery)
	if err == nil {
		// Success - ACK
		if ackErr := delivery.Ack(false); ackErr != nil {
			c.logger.Error("Failed to ACK message", "error", ackErr, "messageId", delivery.MessageId)
		}
		return
	}

	// Handler returned error - determine retry or DLQ
	c.logger.Warn("Handler failed",
		"error", err,
		"messageId", delivery.MessageId,
		"retryCount", retryCount,
		"maxRetries", c.publisher.MaxRetries(),
	)

	maxRetries := c.publisher.MaxRetries()
	if retryCount < maxRetries {
		// ACK and republish to retry queue
		if ackErr := delivery.Ack(false); ackErr != nil {
			c.logger.Error("Failed to ACK before retry", "error", ackErr)
			return
		}

		// Republish with incremented retry count
		if pubErr := c.publishToRetryWithCount(ctx, retryCount, delivery); pubErr != nil {
			c.logger.Error("Failed to publish to retry queue", "error", pubErr, "retryIndex", retryCount)
		} else {
			c.logger.Info("Message queued for retry",
				"messageId", delivery.MessageId,
				"retryIndex", retryCount,
				"nextRetryIndex", retryCount,
			)
		}
	} else {
		// Max retries exhausted - NACK to DLQ
		c.logger.Error("Max retries exhausted, sending to DLQ",
			"messageId", delivery.MessageId,
			"retryCount", retryCount,
		)
		if nackErr := delivery.Nack(false, false); nackErr != nil {
			c.logger.Error("Failed to NACK message", "error", nackErr)
		}
	}
}

// publishToRetryWithCount publishes to retry queue with incremented retry count header
func (c *RabbitMQConsumer) publishToRetryWithCount(ctx context.Context, currentRetry int, delivery amqp.Delivery) error {
	// Create new headers with incremented retry count
	headers := make(amqp.Table)
	for k, v := range delivery.Headers {
		headers[k] = v
	}
	headers[RetryCountHeader] = int32(currentRetry + 1)

	// Use publisher's channel for retry publish
	return c.publisher.PublishToRetryWithHeaders(ctx, currentRetry, delivery.Body, delivery.CorrelationId, headers)
}

// GetRetryCount extracts the retry count from message headers
func GetRetryCount(delivery amqp.Delivery) int {
	if delivery.Headers == nil {
		return 0
	}

	val, ok := delivery.Headers[RetryCountHeader]
	if !ok {
		return 0
	}

	switch v := val.(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// Connection returns the underlying AMQP connection for health checks
func (c *RabbitMQConsumer) Connection() *amqp.Connection {
	return c.conn
}

// Close stops consuming and closes the connection
func (c *RabbitMQConsumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	var errs []error

	if c.channel != nil {
		if err := c.channel.Close(); err != nil {
			errs = append(errs, fmt.Errorf("channel: %v", err))
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("connection: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrCloseFailed, errors.Join(errs...))
	}
	return nil
}
```

**Step 4: Add PublishToRetryWithHeaders to publisher**

Edit `portfolio-common/queue/publisher.go`, add after `PublishToRetry`:

```go
// PublishToRetryWithHeaders sends a message to a retry queue with custom headers.
// Used by consumer to preserve and update retry count header.
func (p *RabbitMQPublisher) PublishToRetryWithHeaders(ctx context.Context, retryIndex int, body []byte, correlationId string, headers amqp.Table) error {
	maxRetries := p.MaxRetries()
	if maxRetries == 0 {
		return fmt.Errorf("%w: no retry queues configured", ErrRetryOutOfBounds)
	}
	if retryIndex < 0 || retryIndex >= maxRetries {
		return fmt.Errorf("%w: index %d, max %d", ErrRetryOutOfBounds, retryIndex, maxRetries-1)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("%w", ErrPublisherClosed)
	}

	err := p.channel.PublishWithContext(ctx, p.exchange, p.retryQueues[retryIndex], false, false,
		amqp.Publishing{
			DeliveryMode:  amqp.Persistent,
			ContentType:   "application/json",
			Body:          body,
			Timestamp:     time.Now(),
			MessageId:     uuid.NewString(),
			CorrelationId: correlationId,
			Headers:       headers,
		},
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPublishFailed, err)
	}
	return nil
}
```

**Step 5: Run tests**

```bash
cd d:/Darbs/git/portfolio/portfolio-common
go test -v ./queue/...
```

Expected: PASS

**Step 6: Run CI checks**

```bash
cd d:/Darbs/git/portfolio/portfolio-common
task ci:all
```

**Step 7: Commit**

```bash
git add queue/consumer.go queue/consumer_test.go queue/publisher.go
git commit -m "Add RabbitMQ consumer with retry support"
```

---

## Task 3: Create messaging-service Config

**Files:**
- Create: `messaging-service/internal/config/config.go`
- Create: `messaging-service/internal/config/config_test.go`

**Step 1: Write config test**

Create `messaging-service/internal/config/config_test.go`:

```go
package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_RequiredFields(t *testing.T) {
	// Set required env vars
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_PORT", "5432")
	os.Setenv("DB_USER", "test")
	os.Setenv("DB_PASSWORD", "test")
	os.Setenv("DB_NAME", "portfolio")
	os.Setenv("RABBITMQ_HOST", "localhost")
	os.Setenv("RABBITMQ_PORT", "5672")
	os.Setenv("RABBITMQ_USER", "guest")
	os.Setenv("RABBITMQ_PASSWORD", "guest")
	os.Setenv("RABBITMQ_EXCHANGE", "contact_messages")
	os.Setenv("RABBITMQ_QUEUE", "contact_messages")
	os.Setenv("AWS_REGION", "eu-north-1")
	os.Setenv("SES_FROM_EMAIL", "test@example.com")

	defer func() {
		os.Unsetenv("DB_HOST")
		os.Unsetenv("DB_PORT")
		os.Unsetenv("DB_USER")
		os.Unsetenv("DB_PASSWORD")
		os.Unsetenv("DB_NAME")
		os.Unsetenv("RABBITMQ_HOST")
		os.Unsetenv("RABBITMQ_PORT")
		os.Unsetenv("RABBITMQ_USER")
		os.Unsetenv("RABBITMQ_PASSWORD")
		os.Unsetenv("RABBITMQ_EXCHANGE")
		os.Unsetenv("RABBITMQ_QUEUE")
		os.Unsetenv("AWS_REGION")
		os.Unsetenv("SES_FROM_EMAIL")
	}()

	cfg := Load()

	assert.Equal(t, "localhost", cfg.DatabaseConfig.Host)
	assert.Equal(t, 5432, cfg.DatabaseConfig.Port)
	assert.Equal(t, "eu-north-1", cfg.AWSRegion)
	assert.Equal(t, "test@example.com", cfg.SESFromEmail)
}
```

**Step 2: Run test to verify it fails**

```bash
cd d:/Darbs/git/portfolio/messaging-service
go test -v ./internal/config/...
```

Expected: FAIL - package doesn't exist

**Step 3: Write config implementation**

Create `messaging-service/internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"

	"github.com/go-playground/validator/v10"

	common "github.com/GunarsK-portfolio/portfolio-common/config"
)

// Config holds all configuration for the messaging service worker
type Config struct {
	common.DatabaseConfig
	common.RabbitMQConfig

	// AWS SES
	AWSRegion    string `validate:"required"`
	SESEndpoint  string // Optional - for LocalStack
	SESFromEmail string `validate:"required,email"`

	// Logging
	LogLevel  string
	LogFormat string
	LogSource bool

	// Service
	Environment string
}

// Load loads all configuration from environment variables
func Load() *Config {
	cfg := &Config{
		DatabaseConfig: common.NewDatabaseConfig(),
		RabbitMQConfig: common.NewRabbitMQConfig(),

		AWSRegion:    common.GetEnvRequired("AWS_REGION"),
		SESEndpoint:  os.Getenv("SES_ENDPOINT"),
		SESFromEmail: common.GetEnvRequired("SES_FROM_EMAIL"),

		LogLevel:  os.Getenv("LOG_LEVEL"),
		LogFormat: os.Getenv("LOG_FORMAT"),
		LogSource: os.Getenv("LOG_SOURCE") == "true",

		Environment: os.Getenv("ENVIRONMENT"),
	}

	validate := validator.New()
	if err := validate.Struct(cfg); err != nil {
		panic(fmt.Sprintf("Invalid configuration: %v", err))
	}

	return cfg
}
```

**Step 4: Add dependencies**

```bash
cd d:/Darbs/git/portfolio/messaging-service
go get github.com/GunarsK-portfolio/portfolio-common@latest
go get github.com/go-playground/validator/v10
go mod tidy
```

**Step 5: Run test**

```bash
go test -v ./internal/config/...
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/config/
git commit -m "Add config package with database, RabbitMQ, and SES settings"
```

---

## Task 4: Create Repository

**Files:**
- Create: `messaging-service/internal/repository/repository.go`
- Create: `messaging-service/internal/repository/repository_test.go`

**Step 1: Write repository test**

Create `messaging-service/internal/repository/repository_test.go`:

```go
package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepositoryInterface(t *testing.T) {
	// Verify interface is implemented
	var _ Repository = (*repository)(nil)
	assert.True(t, true)
}
```

**Step 2: Write repository implementation**

Create `messaging-service/internal/repository/repository.go`:

```go
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/GunarsK-portfolio/portfolio-common/models"
	"gorm.io/gorm"
)

// Repository defines the interface for messaging-service data operations
type Repository interface {
	// Contact Messages
	GetContactMessageByID(ctx context.Context, id int64) (*models.ContactMessage, error)
	UpdateContactMessageStatus(ctx context.Context, id int64, status string, lastError *string) error
	UpdateContactMessageSent(ctx context.Context, id int64) error
	IncrementContactMessageAttempts(ctx context.Context, id int64) error

	// Recipients
	GetActiveRecipients(ctx context.Context) ([]models.Recipient, error)

	// Delivery Attempts
	CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error
	GetSuccessfulDeliveries(ctx context.Context, messageID int64) ([]string, error)
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
		return nil, fmt.Errorf("get contact message %d: %w", id, err)
	}
	return &message, nil
}

// UpdateContactMessageStatus updates the status of a contact message
func (r *repository) UpdateContactMessageStatus(ctx context.Context, id int64, status string, lastError *string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if lastError != nil {
		updates["last_error"] = *lastError
	}
	if status == models.MessageStatusSent {
		updates["sent_at"] = time.Now()
	}

	result := r.db.WithContext(ctx).
		Model(&models.ContactMessage{}).
		Where("id = ?", id).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("update contact message status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("contact message %d not found", id)
	}
	return nil
}

// UpdateContactMessageSent marks message as sent with timestamp
func (r *repository) UpdateContactMessageSent(ctx context.Context, id int64) error {
	return r.UpdateContactMessageStatus(ctx, id, models.MessageStatusSent, nil)
}

// IncrementContactMessageAttempts increments the attempts counter
func (r *repository) IncrementContactMessageAttempts(ctx context.Context, id int64) error {
	result := r.db.WithContext(ctx).
		Model(&models.ContactMessage{}).
		Where("id = ?", id).
		Update("attempts", gorm.Expr("attempts + 1"))

	if result.Error != nil {
		return fmt.Errorf("increment attempts: %w", result.Error)
	}
	return nil
}

// GetActiveRecipients retrieves all active email recipients
func (r *repository) GetActiveRecipients(ctx context.Context) ([]models.Recipient, error) {
	var recipients []models.Recipient
	err := r.db.WithContext(ctx).
		Where("is_active = ?", true).
		Find(&recipients).Error
	if err != nil {
		return nil, fmt.Errorf("get active recipients: %w", err)
	}
	return recipients, nil
}

// CreateDeliveryAttempt records a delivery attempt
func (r *repository) CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error {
	attempt.AttemptedAt = time.Now()
	err := r.db.WithContext(ctx).Create(attempt).Error
	if err != nil {
		return fmt.Errorf("create delivery attempt: %w", err)
	}
	return nil
}

// GetSuccessfulDeliveries returns emails that were successfully delivered for a message
func (r *repository) GetSuccessfulDeliveries(ctx context.Context, messageID int64) ([]string, error) {
	var emails []string
	err := r.db.WithContext(ctx).
		Model(&models.DeliveryAttempt{}).
		Where("message_id = ? AND status = ?", messageID, models.DeliveryStatusSuccess).
		Pluck("recipient_email", &emails).Error
	if err != nil {
		return nil, fmt.Errorf("get successful deliveries: %w", err)
	}
	return emails, nil
}
```

**Step 3: Add GORM dependency**

```bash
go get gorm.io/gorm
go mod tidy
```

**Step 4: Run test**

```bash
go test -v ./internal/repository/...
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/repository/
git commit -m "Add repository for contact messages and delivery attempts"
```

---

## Task 5: Create SES Email Client

**Files:**
- Create: `messaging-service/internal/email/ses.go`
- Create: `messaging-service/internal/email/ses_test.go`

**Step 1: Write email client test**

Create `messaging-service/internal/email/ses_test.go`:

```go
package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatEmailBody(t *testing.T) {
	body := FormatEmailBody("John Doe", "john@example.com", "Hello", "This is a test message.")

	assert.Contains(t, body, "John Doe")
	assert.Contains(t, body, "john@example.com")
	assert.Contains(t, body, "Hello")
	assert.Contains(t, body, "This is a test message.")
}

func TestFormatSubject(t *testing.T) {
	subject := FormatSubject("Test Subject")
	assert.Equal(t, "New Contact Form: Test Subject", subject)
}

func TestEmailClientInterface(t *testing.T) {
	var _ Client = (*SESClient)(nil)
	assert.True(t, true)
}
```

**Step 2: Run test to verify it fails**

```bash
go test -v ./internal/email/...
```

Expected: FAIL

**Step 3: Write SES client implementation**

Create `messaging-service/internal/email/ses.go`:

```go
package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// Client defines the interface for sending emails
type Client interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

// SESClient implements Client using AWS SES
type SESClient struct {
	client    *ses.Client
	fromEmail string
}

// Config holds SES client configuration
type Config struct {
	Region    string
	Endpoint  string // Optional - for LocalStack
	FromEmail string
}

// NewSESClient creates a new SES email client
func NewSESClient(ctx context.Context, cfg Config) (*SESClient, error) {
	var opts []func(*config.LoadOptions) error
	opts = append(opts, config.WithRegion(cfg.Region))

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	var sesOpts []func(*ses.Options)
	if cfg.Endpoint != "" {
		sesOpts = append(sesOpts, func(o *ses.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}

	return &SESClient{
		client:    ses.NewFromConfig(awsCfg, sesOpts...),
		fromEmail: cfg.FromEmail,
	}, nil
}

// SendEmail sends an email via SES
func (c *SESClient) SendEmail(ctx context.Context, to, subject, body string) error {
	input := &ses.SendEmailInput{
		Source: aws.String(c.fromEmail),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Message: &types.Message{
			Subject: &types.Content{
				Data:    aws.String(subject),
				Charset: aws.String("UTF-8"),
			},
			Body: &types.Body{
				Text: &types.Content{
					Data:    aws.String(body),
					Charset: aws.String("UTF-8"),
				},
			},
		},
	}

	_, err := c.client.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("send email to %s: %w", to, err)
	}

	return nil
}

// FormatSubject formats the email subject
func FormatSubject(originalSubject string) string {
	return fmt.Sprintf("New Contact Form: %s", originalSubject)
}

// FormatEmailBody formats the email body from contact message fields
func FormatEmailBody(name, email, subject, message string) string {
	return fmt.Sprintf(`New contact form submission:

Name: %s
Email: %s
Subject: %s

Message:
%s

---
This is an automated message from the portfolio contact form.
`, name, email, subject, message)
}
```

**Step 4: Add AWS SDK dependency**

```bash
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/service/ses
go mod tidy
```

**Step 5: Run test**

```bash
go test -v ./internal/email/...
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/email/
git commit -m "Add SES email client with formatting helpers"
```

---

## Task 6: Create Message Handler

**Files:**
- Create: `messaging-service/internal/handler/handler.go`
- Create: `messaging-service/internal/handler/handler_test.go`

**Step 1: Write handler test**

Create `messaging-service/internal/handler/handler_test.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/GunarsK-portfolio/portfolio-common/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRepository implements repository.Repository for testing
type MockRepository struct {
	mock.Mock
}

func (m *MockRepository) GetContactMessageByID(ctx context.Context, id int64) (*models.ContactMessage, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ContactMessage), args.Error(1)
}

func (m *MockRepository) UpdateContactMessageStatus(ctx context.Context, id int64, status string, lastError *string) error {
	args := m.Called(ctx, id, status, lastError)
	return args.Error(0)
}

func (m *MockRepository) UpdateContactMessageSent(ctx context.Context, id int64) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRepository) IncrementContactMessageAttempts(ctx context.Context, id int64) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRepository) GetActiveRecipients(ctx context.Context) ([]models.Recipient, error) {
	args := m.Called(ctx)
	return args.Get(0).([]models.Recipient), args.Error(1)
}

func (m *MockRepository) CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error {
	args := m.Called(ctx, attempt)
	return args.Error(0)
}

func (m *MockRepository) GetSuccessfulDeliveries(ctx context.Context, messageID int64) ([]string, error) {
	args := m.Called(ctx, messageID)
	return args.Get(0).([]string), args.Error(1)
}

// MockEmailClient implements email.Client for testing
type MockEmailClient struct {
	mock.Mock
}

func (m *MockEmailClient) SendEmail(ctx context.Context, to, subject, body string) error {
	args := m.Called(ctx, to, subject, body)
	return args.Error(0)
}

func TestHandler_ProcessMessage_Success(t *testing.T) {
	mockRepo := new(MockRepository)
	mockEmail := new(MockEmailClient)
	h := New(mockRepo, mockEmail, nil)

	message := &models.ContactMessage{
		ID:      1,
		Name:    "John",
		Email:   "john@example.com",
		Subject: "Test",
		Message: "Hello",
		Status:  models.MessageStatusQueued,
	}

	recipients := []models.Recipient{
		{Email: "admin@example.com", IsActive: true},
	}

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	mockRepo.On("GetContactMessageByID", mock.Anything, int64(1)).Return(message, nil)
	mockRepo.On("GetActiveRecipients", mock.Anything).Return(recipients, nil)
	mockRepo.On("GetSuccessfulDeliveries", mock.Anything, int64(1)).Return([]string{}, nil)
	mockEmail.On("SendEmail", mock.Anything, "admin@example.com", mock.Anything, mock.Anything).Return(nil)
	mockRepo.On("CreateDeliveryAttempt", mock.Anything, mock.Anything).Return(nil)
	mockRepo.On("UpdateContactMessageSent", mock.Anything, int64(1)).Return(nil)

	err := h.HandleMessage(context.Background(), delivery)

	assert.NoError(t, err)
	mockRepo.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
}

func TestHandler_ProcessMessage_SkipsAlreadySent(t *testing.T) {
	mockRepo := new(MockRepository)
	mockEmail := new(MockEmailClient)
	h := New(mockRepo, mockEmail, nil)

	message := &models.ContactMessage{
		ID:     1,
		Status: models.MessageStatusSent, // Already sent
	}

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	mockRepo.On("GetContactMessageByID", mock.Anything, int64(1)).Return(message, nil)

	err := h.HandleMessage(context.Background(), delivery)

	assert.NoError(t, err) // No error, just skip
	mockEmail.AssertNotCalled(t, "SendEmail")
}

func TestHandler_ProcessMessage_PartialFailure(t *testing.T) {
	mockRepo := new(MockRepository)
	mockEmail := new(MockEmailClient)
	h := New(mockRepo, mockEmail, nil)

	message := &models.ContactMessage{
		ID:      1,
		Name:    "John",
		Email:   "john@example.com",
		Subject: "Test",
		Message: "Hello",
		Status:  models.MessageStatusQueued,
	}

	recipients := []models.Recipient{
		{Email: "admin1@example.com", IsActive: true},
		{Email: "admin2@example.com", IsActive: true},
	}

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	mockRepo.On("GetContactMessageByID", mock.Anything, int64(1)).Return(message, nil)
	mockRepo.On("GetActiveRecipients", mock.Anything).Return(recipients, nil)
	mockRepo.On("GetSuccessfulDeliveries", mock.Anything, int64(1)).Return([]string{}, nil)
	mockEmail.On("SendEmail", mock.Anything, "admin1@example.com", mock.Anything, mock.Anything).Return(nil)
	mockEmail.On("SendEmail", mock.Anything, "admin2@example.com", mock.Anything, mock.Anything).Return(errors.New("SES error"))
	mockRepo.On("CreateDeliveryAttempt", mock.Anything, mock.Anything).Return(nil)
	mockRepo.On("IncrementContactMessageAttempts", mock.Anything, int64(1)).Return(nil)

	err := h.HandleMessage(context.Background(), delivery)

	assert.Error(t, err) // Returns error to trigger retry
	assert.Contains(t, err.Error(), "1 of 2 recipients failed")
}

func TestHandler_ProcessMessage_SkipsAlreadyDelivered(t *testing.T) {
	mockRepo := new(MockRepository)
	mockEmail := new(MockEmailClient)
	h := New(mockRepo, mockEmail, nil)

	message := &models.ContactMessage{
		ID:      1,
		Name:    "John",
		Email:   "john@example.com",
		Subject: "Test",
		Message: "Hello",
		Status:  models.MessageStatusQueued,
	}

	recipients := []models.Recipient{
		{Email: "admin1@example.com", IsActive: true},
		{Email: "admin2@example.com", IsActive: true},
	}

	event := models.ContactMessageEvent{MessageID: 1}
	body, _ := json.Marshal(event)
	delivery := amqp.Delivery{Body: body}

	// admin1 already delivered successfully
	mockRepo.On("GetContactMessageByID", mock.Anything, int64(1)).Return(message, nil)
	mockRepo.On("GetActiveRecipients", mock.Anything).Return(recipients, nil)
	mockRepo.On("GetSuccessfulDeliveries", mock.Anything, int64(1)).Return([]string{"admin1@example.com"}, nil)
	mockEmail.On("SendEmail", mock.Anything, "admin2@example.com", mock.Anything, mock.Anything).Return(nil)
	mockRepo.On("CreateDeliveryAttempt", mock.Anything, mock.Anything).Return(nil)
	mockRepo.On("UpdateContactMessageSent", mock.Anything, int64(1)).Return(nil)

	err := h.HandleMessage(context.Background(), delivery)

	assert.NoError(t, err)
	// admin1 should NOT be called
	mockEmail.AssertNumberOfCalls(t, "SendEmail", 1)
}
```

**Step 2: Run test to verify it fails**

```bash
go test -v ./internal/handler/...
```

Expected: FAIL

**Step 3: Write handler implementation**

Create `messaging-service/internal/handler/handler.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/GunarsK-portfolio/messaging-service/internal/email"
	"github.com/GunarsK-portfolio/messaging-service/internal/repository"
	"github.com/GunarsK-portfolio/portfolio-common/models"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Handler processes contact message events
type Handler struct {
	repo   repository.Repository
	email  email.Client
	logger *slog.Logger
}

// New creates a new message handler
func New(repo repository.Repository, emailClient email.Client, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		repo:   repo,
		email:  emailClient,
		logger: logger,
	}
}

// HandleMessage processes a single contact message event from RabbitMQ.
// Returns nil on success, error to trigger retry.
func (h *Handler) HandleMessage(ctx context.Context, delivery amqp.Delivery) error {
	// Parse event
	var event models.ContactMessageEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		h.logger.Error("Failed to parse message event", "error", err)
		return nil // ACK malformed messages, don't retry
	}

	h.logger.Info("Processing contact message", "messageId", event.MessageID)

	// Fetch message from DB
	message, err := h.repo.GetContactMessageByID(ctx, event.MessageID)
	if err != nil {
		h.logger.Error("Failed to fetch message", "messageId", event.MessageID, "error", err)
		return nil // Message deleted, don't retry
	}

	// Skip if already processed
	if message.Status != models.MessageStatusQueued {
		h.logger.Info("Message already processed, skipping",
			"messageId", event.MessageID,
			"status", message.Status,
		)
		return nil
	}

	// Get active recipients
	recipients, err := h.repo.GetActiveRecipients(ctx)
	if err != nil {
		h.logger.Error("Failed to fetch recipients", "error", err)
		return fmt.Errorf("fetch recipients: %w", err)
	}

	if len(recipients) == 0 {
		h.logger.Warn("No active recipients, marking as sent", "messageId", event.MessageID)
		return h.repo.UpdateContactMessageSent(ctx, event.MessageID)
	}

	// Get already-delivered recipients (for idempotency on retry)
	alreadyDelivered, err := h.repo.GetSuccessfulDeliveries(ctx, event.MessageID)
	if err != nil {
		h.logger.Error("Failed to fetch successful deliveries", "error", err)
		return fmt.Errorf("fetch deliveries: %w", err)
	}

	deliveredSet := make(map[string]bool)
	for _, e := range alreadyDelivered {
		deliveredSet[e] = true
	}

	// Send emails
	subject := email.FormatSubject(message.Subject)
	body := email.FormatEmailBody(message.Name, message.Email, message.Subject, message.Message)

	var failedCount int
	var successCount int

	for _, recipient := range recipients {
		// Skip already-delivered
		if deliveredSet[recipient.Email] {
			h.logger.Debug("Skipping already-delivered recipient",
				"messageId", event.MessageID,
				"recipient", recipient.Email,
			)
			successCount++
			continue
		}

		sendErr := h.email.SendEmail(ctx, recipient.Email, subject, body)

		attempt := &models.DeliveryAttempt{
			MessageID:      event.MessageID,
			RecipientEmail: recipient.Email,
		}

		if sendErr != nil {
			h.logger.Error("Failed to send email",
				"messageId", event.MessageID,
				"recipient", recipient.Email,
				"error", sendErr,
			)
			attempt.Status = models.DeliveryStatusFailed
			errMsg := sendErr.Error()
			attempt.ErrorMessage = &errMsg
			failedCount++
		} else {
			h.logger.Info("Email sent successfully",
				"messageId", event.MessageID,
				"recipient", recipient.Email,
			)
			attempt.Status = models.DeliveryStatusSuccess
			successCount++
		}

		// Record delivery attempt
		if recordErr := h.repo.CreateDeliveryAttempt(ctx, attempt); recordErr != nil {
			h.logger.Error("Failed to record delivery attempt", "error", recordErr)
		}
	}

	// Evaluate results
	if failedCount == 0 {
		// All successful
		h.logger.Info("All emails sent successfully",
			"messageId", event.MessageID,
			"count", successCount,
		)
		return h.repo.UpdateContactMessageSent(ctx, event.MessageID)
	}

	// Some failed - increment attempts and return error for retry
	if err := h.repo.IncrementContactMessageAttempts(ctx, event.MessageID); err != nil {
		h.logger.Error("Failed to increment attempts", "error", err)
	}

	return fmt.Errorf("%d of %d recipients failed", failedCount, len(recipients))
}
```

**Step 4: Add testify dependency**

```bash
go get github.com/stretchr/testify
go get github.com/rabbitmq/amqp091-go
go mod tidy
```

**Step 5: Run test**

```bash
go test -v ./internal/handler/...
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/handler/
git commit -m "Add message handler with retry and idempotency support"
```

---

## Task 7: Create Main Worker Entrypoint

**Files:**
- Create: `messaging-service/cmd/worker/main.go`

**Step 1: Write main.go**

Create `messaging-service/cmd/worker/main.go`:

```go
package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/GunarsK-portfolio/messaging-service/internal/config"
	"github.com/GunarsK-portfolio/messaging-service/internal/email"
	"github.com/GunarsK-portfolio/messaging-service/internal/handler"
	"github.com/GunarsK-portfolio/messaging-service/internal/repository"
	commondb "github.com/GunarsK-portfolio/portfolio-common/database"
	"github.com/GunarsK-portfolio/portfolio-common/logger"
	"github.com/GunarsK-portfolio/portfolio-common/queue"
)

func main() {
	cfg := config.Load()

	appLogger := logger.New(logger.Config{
		Level:       cfg.LogLevel,
		Format:      cfg.LogFormat,
		ServiceName: "messaging-service",
		AddSource:   cfg.LogSource,
	})

	appLogger.Info("Starting messaging service worker", "environment", cfg.Environment)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		appLogger.Info("Received shutdown signal", "signal", sig)
		cancel()
	}()

	// Database connection
	db, err := commondb.Connect(commondb.PostgresConfig{
		Host:     cfg.DatabaseConfig.Host,
		Port:     strconv.Itoa(cfg.DatabaseConfig.Port),
		User:     cfg.DatabaseConfig.User,
		Password: cfg.DatabaseConfig.Password,
		DBName:   cfg.DatabaseConfig.Name,
		SSLMode:  cfg.DatabaseConfig.SSLMode,
		TimeZone: "UTC",
	})
	if err != nil {
		appLogger.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := commondb.CloseDB(db); closeErr != nil {
			appLogger.Error("Failed to close database", "error", closeErr)
		}
	}()
	appLogger.Info("Database connection established")

	// RabbitMQ publisher (for retry/DLQ access)
	publisher, err := queue.NewRabbitMQPublisher(cfg.RabbitMQConfig)
	if err != nil {
		appLogger.Error("Failed to create RabbitMQ publisher", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := publisher.Close(); closeErr != nil {
			appLogger.Error("Failed to close RabbitMQ publisher", "error", closeErr)
		}
	}()
	appLogger.Info("RabbitMQ publisher created")

	// RabbitMQ consumer
	consumerCfg := queue.DefaultConsumerConfig("messaging-service")
	consumer, err := queue.NewRabbitMQConsumer(cfg.RabbitMQConfig, publisher, consumerCfg, appLogger.Logger)
	if err != nil {
		appLogger.Error("Failed to create RabbitMQ consumer", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := consumer.Close(); closeErr != nil {
			appLogger.Error("Failed to close RabbitMQ consumer", "error", closeErr)
		}
	}()
	appLogger.Info("RabbitMQ consumer created")

	// SES email client
	emailClient, err := email.NewSESClient(ctx, email.Config{
		Region:    cfg.AWSRegion,
		Endpoint:  cfg.SESEndpoint,
		FromEmail: cfg.SESFromEmail,
	})
	if err != nil {
		appLogger.Error("Failed to create SES client", "error", err)
		os.Exit(1)
	}
	appLogger.Info("SES email client created")

	// Repository
	repo := repository.New(db)

	// Message handler
	msgHandler := handler.New(repo, emailClient, appLogger)

	// Start consuming
	appLogger.Info("Starting message consumer",
		"queue", cfg.RabbitMQConfig.Queue,
		"maxRetries", publisher.MaxRetries(),
	)

	if err := consumer.Consume(ctx, msgHandler.HandleMessage); err != nil {
		if err != context.Canceled {
			appLogger.Error("Consumer error", "error", err)
			os.Exit(1)
		}
	}

	appLogger.Info("Messaging service worker stopped")
}
```

**Step 2: Verify it compiles**

```bash
go build -o bin/messaging-service ./cmd/worker
```

**Step 3: Commit**

```bash
git add cmd/worker/
git commit -m "Add main worker entrypoint with graceful shutdown"
```

---

## Task 8: Create Dockerfile and Taskfile

**Files:**
- Create: `messaging-service/Dockerfile`
- Create: `messaging-service/Taskfile.yml`

**Step 1: Create Dockerfile**

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o service ./cmd/worker

# Production stage
FROM alpine:3.22

# Security updates and ca-certificates for HTTPS
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 app && adduser -D -u 1000 -G app app

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/service .

# Set ownership
RUN chown -R app:app /app

# Switch to non-root user
USER app

# No port exposed - background worker

# Run the binary
CMD ["./service"]
```

**Step 2: Create Taskfile.yml**

```yaml
version: '3'

tasks:
  # Top-level tasks
  run:
    desc: Run the messaging service worker locally
    cmds:
      - go run cmd/worker/main.go

  build:
    desc: Build the messaging service binary
    cmds:
      - go build -o bin/messaging-service cmd/worker/main.go

  test:
    desc: Run tests
    cmds:
      - go test -v ./...

  clean:
    desc: Clean build artifacts
    cmds:
      - cmd: rm -rf bin/ coverage.out coverage.html gosec-report.json
        platforms: [linux, darwin]
      - cmd: if exist bin rmdir /s /q bin
        platforms: [windows]
        ignore_error: true
      - cmd: if exist coverage.out del coverage.out
        platforms: [windows]
        ignore_error: true
      - cmd: if exist coverage.html del coverage.html
        platforms: [windows]
        ignore_error: true
      - cmd: if exist gosec-report.json del gosec-report.json
        platforms: [windows]
        ignore_error: true

  # Docker operations
  docker:build:
    desc: Build Docker image
    cmds:
      - docker build -t messaging-service .

  # Development tools
  dev:install-tools:
    desc: Install development and CI tools
    cmds:
      - go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0
      - go install golang.org/x/vuln/cmd/govulncheck@latest
      - go install github.com/securego/gosec/v2/cmd/gosec@latest
      - go install golang.org/x/tools/cmd/goimports@latest
      - go install github.com/gordonklaus/ineffassign@latest
      - echo "All development tools installed successfully!"

  # Code quality
  lint:
    desc: Run golangci-lint
    cmds:
      - go mod download
      - golangci-lint run --timeout=5m

  format:
    desc: Format code with gofmt
    cmds:
      - gofmt -w .

  vet:
    desc: Run go vet
    cmds:
      - go vet ./...

  tidy:
    desc: Tidy and verify go.mod
    cmds:
      - go mod tidy
      - go mod verify

  # Testing
  test:coverage:
    desc: Run tests with coverage report
    cmds:
      - go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
      - go tool cover -html=coverage.out -o coverage.html
      - echo "Coverage report generated at coverage.html"

  # Security
  security:scan:
    desc: Run gosec security scanner
    cmds:
      - gosec -fmt=json -out=gosec-report.json ./...
      - echo "Security report saved to gosec-report.json"

  security:vuln:
    desc: Check for known vulnerabilities with govulncheck
    cmds:
      - govulncheck ./...

  # Linting
  lint:markdown:
    desc: Lint Markdown files
    cmds:
      - npx markdownlint-cli2

  # CI/CD
  ci:all:
    desc: Run all CI checks (format, tidy, lint, vet, test, vuln, lint:markdown)
    cmds:
      - task: format
      - task: tidy
      - task: lint
      - task: vet
      - task: test
      - task: security:vuln
      - task: lint:markdown
      - echo "All CI checks passed!"
```

**Step 3: Create .golangci.yml**

```yaml
version: "2"

linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gofmt
    - goimports
    - misspell
    - unconvert
    - unparam

linters-settings:
  gofmt:
    simplify: true
  goimports:
    local-prefixes: github.com/GunarsK-portfolio

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - errcheck
        - unparam
```

**Step 4: Create .markdownlint.yaml**

```yaml
default: true
MD013: false
MD033: false
```

**Step 5: Commit**

```bash
git add Dockerfile Taskfile.yml .golangci.yml .markdownlint.yaml
git commit -m "Add Dockerfile and Taskfile for CI/build"
```

---

## Task 9: Add messaging-service to Docker Compose

**Files:**
- Modify: `infrastructure/docker-compose.yml`

**Step 1: Add messaging-service container**

Add after `messaging-api` service block in `infrastructure/docker-compose.yml`:

```yaml
  # Messaging Service (background worker)
  messaging-service:
    build:
      context: ../messaging-service
      dockerfile: Dockerfile
    container_name: messaging-service
    restart: unless-stopped
    environment:
      - DB_HOST=${DB_HOST}
      - DB_PORT=${DB_PORT}
      - DB_USER=${DB_USER_MESSAGING}
      - DB_PASSWORD=${DB_PASSWORD_MESSAGING}
      - DB_NAME=${POSTGRES_DB}
      - DB_SSLMODE=disable
      - RABBITMQ_HOST=${RABBITMQ_HOST}
      - RABBITMQ_PORT=${RABBITMQ_PORT}
      - RABBITMQ_USER=${RABBITMQ_USER}
      - RABBITMQ_PASSWORD=${RABBITMQ_PASSWORD}
      - RABBITMQ_EXCHANGE=${RABBITMQ_EXCHANGE}
      - RABBITMQ_QUEUE=${RABBITMQ_QUEUE}
      - RABBITMQ_RETRY_DELAYS=${RABBITMQ_RETRY_DELAYS}
      - AWS_REGION=${AWS_REGION}
      - SES_ENDPOINT=${SES_ENDPOINT}
      - SES_FROM_EMAIL=${SES_FROM_EMAIL}
      - LOG_LEVEL=${LOG_LEVEL}
      - LOG_FORMAT=${LOG_FORMAT}
      - LOG_SOURCE=${LOG_SOURCE}
      - ENVIRONMENT=${ENVIRONMENT}
    networks:
      - network
    depends_on:
      postgres:
        condition: service_healthy
      rabbitmq:
        condition: service_healthy
      localstack:
        condition: service_healthy
      flyway:
        condition: service_completed_successfully
    deploy:
      resources:
        limits:
          memory: 128M
        reservations:
          memory: 64M
```

**Step 2: Add messaging-service to swagger dependencies**

In the `swagger` service, add `messaging-service` to depends_on list.

**Step 3: Commit**

```bash
cd ../infrastructure
git add docker-compose.yml
git commit -m "Add messaging-service worker to docker-compose"
```

---

## Task 10: Run CI and Test Locally

**Step 1: Run CI checks on portfolio-common**

```bash
cd d:/Darbs/git/portfolio/portfolio-common
task ci:all
```

**Step 2: Run CI checks on messaging-service**

```bash
cd d:/Darbs/git/portfolio/messaging-service
task ci:all
```

**Step 3: Start docker-compose and test**

```bash
cd d:/Darbs/git/portfolio/infrastructure
docker-compose up -d --build messaging-service
docker-compose logs -f messaging-service
```

**Step 4: Submit a test contact message**

Use the public-web contact form or curl:

```bash
curl -X POST https://localhost/message/v1/contact \
  -H "Content-Type: application/json" \
  -d '{"name":"Test","email":"test@example.com","subject":"Test","message":"Hello"}'
```

**Step 5: Verify email sent**

Check LocalStack SES logs or messaging-service logs for successful delivery.

**Step 6: Push commits**

```bash
cd d:/Darbs/git/portfolio/messaging-service
git push -u origin main

cd d:/Darbs/git/portfolio/portfolio-common
git push

cd d:/Darbs/git/portfolio/infrastructure
git push
```

---

## Task 11: Add Terraform Configuration (AWS Production)

**Files:**
- Modify: `infrastructure/terraform/main.tf`
- Modify: `infrastructure/terraform/modules/app-runner/main.tf` (if needed)

This task adds:
1. ECR repository for messaging-service
2. App Runner service configuration
3. IAM policy for SES permissions

Details depend on existing Terraform structure. Follow patterns from other services (auth-service, files-api).

---

## Summary

| Task | Component | Files |
|------|-----------|-------|
| 1 | Initialize repo | go.mod, .gitignore, README.md |
| 2 | RabbitMQ Consumer | portfolio-common/queue/consumer.go |
| 3 | Config | internal/config/config.go |
| 4 | Repository | internal/repository/repository.go |
| 5 | SES Client | internal/email/ses.go |
| 6 | Handler | internal/handler/handler.go |
| 7 | Main | cmd/worker/main.go |
| 8 | Build | Dockerfile, Taskfile.yml |
| 9 | Docker Compose | infrastructure/docker-compose.yml |
| 10 | CI/Test | - |
| 11 | Terraform | infrastructure/terraform/*.tf |
