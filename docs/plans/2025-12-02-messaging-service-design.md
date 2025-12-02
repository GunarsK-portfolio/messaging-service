# messaging-service Design

## Overview

Background worker that consumes contact form messages from RabbitMQ and sends email notifications via SES.

**Flow**: RabbitMQ queue → messaging-service → SES → email to recipients

## Architecture

### Component Structure

```
portfolio-common/
└── queue/
    ├── publisher.go      (existing)
    └── consumer.go       (new - RabbitMQ consumer)

messaging-service/
├── cmd/worker/main.go
├── internal/
│   ├── config/config.go
│   ├── handler/handler.go     # Message processing logic
│   ├── email/ses.go           # SES client
│   └── repository/repository.go
├── Dockerfile
└── go.mod
```

### Dependencies

- PostgreSQL (messaging schema)
- RabbitMQ (contact_messages queue)
- SES (LocalStack locally, AWS in prod)

## portfolio-common Consumer

### Interface

```go
type MessageHandler func(ctx context.Context, delivery amqp.Delivery) error

type Consumer interface {
    Consume(ctx context.Context, handler MessageHandler) error
    Close() error
}
```

### RabbitMQConsumer

```go
type RabbitMQConsumer struct {
    conn       *amqp.Connection
    channel    *amqp.Channel
    publisher  *RabbitMQPublisher  // For retry/DLQ access
    queueName  string
    logger     *slog.Logger
}

func NewRabbitMQConsumer(cfg RabbitMQConfig, publisher *RabbitMQPublisher, logger *slog.Logger) (*RabbitMQConsumer, error)
```

The consumer holds a reference to the publisher to access:
- `PublishToRetry(ctx, retryIndex, body, correlationId)` - explicit retry
- `PublishToDLQ(ctx, body, correlationId)` - dead letter
- `MaxRetries()` - retry count limit

### Retry Tracking

Retry count tracked via AMQP header `x-retry-count`:
- Initial message: header absent (count = 0)
- Each `PublishToRetry()` increments header
- Consumer reads header to determine current attempt

## Message Processing Flow

```
1. Consume delivery from contact_messages queue
2. Parse ContactMessageEvent{MessageID int64}
3. Fetch ContactMessage from DB by ID
4. If message status != "queued" → ACK and skip (already processed)
5. Fetch active Recipients from DB
6. Query delivery_attempts for this message_id with status='success'
7. Filter out already-delivered recipients
8. For each remaining recipient:
   a. Send email via SES
   b. Record DeliveryAttempt (success or failed)
9. Evaluate results:
   - ALL successful → ACK, update status to "sent", set sent_at
   - ANY failed:
     - Read x-retry-count header
     - If < MaxRetries → ACK + PublishToRetry(retryIndex), increment attempts
     - If >= MaxRetries → NACK (routes to DLQ), update status to "failed"
```

### Idempotency

On retry, the worker:
1. Queries `delivery_attempts WHERE message_id=X AND status='success'`
2. Skips recipients with successful delivery
3. Only attempts remaining recipients

This prevents duplicate emails across retries.

## SES Client

```go
type EmailClient interface {
    SendEmail(ctx context.Context, to, subject, body string) error
}

type SESClient struct {
    client    *ses.Client
    fromEmail string
}
```

- Uses AWS SDK v2
- LocalStack: endpoint overridden via `SES_ENDPOINT`
- Production: uses default AWS SES endpoint

### Email Format

- **From**: `SES_FROM_EMAIL`
- **To**: Each active recipient
- **Subject**: "New Contact Form: {original subject}"
- **Body**: Plain text with name, email, subject, message

## Configuration

```go
type Config struct {
    common.DatabaseConfig
    common.RabbitMQConfig

    // SES
    AWSRegion    string `validate:"required"`
    SESEndpoint  string // Optional, for LocalStack
    SESFromEmail string `validate:"required,email"`

    // Service
    Environment string
    LogLevel    string
    LogFormat   string
    LogSource   bool
}
```

### Environment Variables

```
# Database
DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE

# RabbitMQ
RABBITMQ_HOST, RABBITMQ_PORT, RABBITMQ_USER, RABBITMQ_PASSWORD
RABBITMQ_EXCHANGE, RABBITMQ_QUEUE, RABBITMQ_RETRY_DELAYS

# SES
AWS_REGION=eu-north-1
SES_ENDPOINT=http://localstack:4566  # Local only
SES_FROM_EMAIL=noreply@localhost

# Logging
LOG_LEVEL, LOG_FORMAT, LOG_SOURCE, ENVIRONMENT
```

## Repository

```go
type Repository interface {
    GetContactMessageByID(ctx context.Context, id int64) (*models.ContactMessage, error)
    UpdateContactMessageStatus(ctx context.Context, id int64, status string, lastError *string) error
    UpdateContactMessageSent(ctx context.Context, id int64) error
    IncrementContactMessageAttempts(ctx context.Context, id int64) error

    GetActiveRecipients(ctx context.Context) ([]models.Recipient, error)

    CreateDeliveryAttempt(ctx context.Context, attempt *models.DeliveryAttempt) error
    GetSuccessfulDeliveries(ctx context.Context, messageID int64) ([]string, error)  // Returns emails
}
```

## Docker Compose

```yaml
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
```

No ports exposed - background worker only.

## Graceful Shutdown

1. Catch SIGINT/SIGTERM
2. Stop consuming new messages
3. Wait for in-flight message to complete (with timeout)
4. Close RabbitMQ connection
5. Close DB connection
6. Exit

## Health Checks

Optional: minimal HTTP server on internal port for health checks, or rely on container restart policy and logging.

For simplicity, start without HTTP health endpoint. Use `restart: unless-stopped` and monitor logs.

## Error Handling

| Error Type | Action |
|------------|--------|
| DB connection failure | Log, retry with backoff |
| RabbitMQ connection failure | Log, retry with backoff |
| Message parse error | ACK (discard malformed), log error |
| Message not found in DB | ACK (already deleted), log warning |
| SES temporary error | Retry via PublishToRetry |
| SES permanent error (invalid email) | Record as failed, continue to next recipient |
| All retries exhausted | NACK to DLQ, mark message as failed |

## Status Transitions

```
pending → queued (by messaging-api on publish)
queued → sent (all emails delivered)
queued → failed (max retries exhausted)
```

## Terraform (AWS Production)

Add messaging-service to infrastructure/terraform:

1. **App Runner service** - New module instance in `main.tf`
   - ECR image source
   - VPC connector for private networking
   - Environment variables from Secrets Manager
   - Health check configuration

2. **IAM role** - SES send permissions
   - `ses:SendEmail`
   - `ses:SendRawEmail`

3. **Environment variables** via App Runner config:
   - Database credentials from Secrets Manager
   - RabbitMQ credentials from Secrets Manager (Amazon MQ)
   - SES configuration (region, from email)
   - No CloudFront/WAF needed (no public endpoint)

4. **ECR repository** - For Docker image

## Testing Strategy

1. Unit tests for handler logic (mock repository, email client)
2. Integration tests with test containers (RabbitMQ, PostgreSQL)
3. LocalStack for SES testing locally
