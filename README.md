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
