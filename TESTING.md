# Testing Guide

## Overview

The messaging-service uses Go's standard `testing` package for unit tests.
This is a background worker service that consumes contact form messages from
RabbitMQ and sends email notifications via AWS SES.

Current coverage: 96.3% on handler

## Quick Commands

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run handler tests only
go test -v ./internal/handler/

# Generate coverage report
go test -coverprofile=coverage.out ./internal/handler/
go tool cover -html=coverage.out -o coverage.html

# Run specific test
go test -v -run TestProcess_Success ./internal/handler/

# Run all error case tests
go test -v -run TestProcess_.*Error ./internal/handler/
```

## Test Files

**`internal/handler/`** - 16 tests

| Category | Tests | Coverage |
|----------|-------|----------|
| Constructor | 1 | Handler initialization |
| Process Success | 3 | Full flow, single recipient, skip already sent |
| Process Errors | 6 | Invalid JSON, not found, no recipients, failures |
| Delivery Attempts | 3 | Success/failure recording, continue on record error |
| Email Content | 1 | Subject and body formatting |
| Context | 1 | Context propagation to repository |
| Helpers | 1 | formatEmailBody field coverage |

Tests are organized in: `handler_test.go`, `mocks_test.go`.

## Key Testing Patterns

**Mock Repository**: Function fields allow per-test behavior customization

```go
mockRepo := &mockRepository{
    getContactMessageByIDFunc: func(
        ctx context.Context, id int64,
    ) (*models.ContactMessage, error) {
        return createTestContactMessage(), nil
    },
    getActiveRecipientsFunc: func(
        ctx context.Context,
    ) ([]models.Recipient, error) {
        return createTestRecipients(), nil
    },
}
```

**Mock Email Client**: Simulates SES behavior

```go
mockEmail := &mockEmailClient{
    sendEmailFunc: func(
        ctx context.Context, to, subject, body string,
    ) error {
        return nil // success
    },
}
```

**AMQP Delivery**: Uses real amqp.Delivery with JSON body

```go
event := models.ContactMessageEvent{MessageID: 1}
body, _ := json.Marshal(event)
delivery := amqp.Delivery{Body: body}

err := handler.Process(context.Background(), delivery)
```

**Test Helpers**: Factory functions for consistent test data

```go
message := createTestContactMessage()
recipient := createTestRecipient()
recipients := createTestRecipients()
```

## Test Categories

### Success Cases

- Full message processing with multiple recipients
- Single recipient delivery
- Partial failure (some emails succeed) still marks as sent
- Skip processing for already-sent messages

### Error Cases

- Invalid JSON in delivery body
- Message not found in database
- No active recipients configured
- Database error fetching recipients
- All email deliveries fail

### Edge Cases

- Delivery attempt recording failure doesn't fail overall process
- Context propagation through all layers
- Email body contains all required fields

## Service Characteristics

The messaging-service is a worker (not HTTP API):

- **Input**: AMQP delivery from RabbitMQ
- **Processing**: Fetch message → Get recipients → Send emails → Record attempts
- **Output**: Message status update (sent/failed)

### Message Flow

1. Receive `ContactMessageEvent` from queue
2. Fetch message details from PostgreSQL
3. Check if already sent (idempotency)
4. Get active recipients
5. Send email to each recipient via SES
6. Record delivery attempt for each
7. Update message status based on results

### Retry Behavior

- At least one successful email = message marked "sent"
- All emails fail = message marked "failed" with error
- Recording delivery attempts can fail without failing the overall process

## Contributing Tests

1. Follow naming: `Test<FunctionName>_<Scenario>`
2. Use section markers for organization
3. Mock only the repository/email methods needed for each test
4. Verify coverage: `go test -cover ./internal/handler/`
5. Run CI: `task ci:all`
