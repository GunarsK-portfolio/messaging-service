# Messaging Service

![CI](https://github.com/GunarsK-portfolio/messaging-service/workflows/CI/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/GunarsK-portfolio/messaging-service)](https://goreportcard.com/report/github.com/GunarsK-portfolio/messaging-service)
[![codecov](https://codecov.io/gh/GunarsK-portfolio/messaging-service/graph/badge.svg)](https://codecov.io/gh/GunarsK-portfolio/messaging-service)
[![CodeRabbit](https://img.shields.io/coderabbit/prs/github/GunarsK-portfolio/messaging-service?label=CodeRabbit&color=2ea44f)](https://coderabbit.ai)

Background worker that consumes contact form messages from RabbitMQ and sends
email notifications via AWS SES.

## Features

- Consumes `ContactMessageEvent` from `contact_messages` queue
- Fetches message details and active recipients from PostgreSQL
- Sends emails via SES (LocalStack locally, AWS SES in production)
- Tracks delivery attempts for idempotency
- Retry with exponential backoff (1m, 5m, 30m, 2h, 12h)
- Graceful shutdown handling

## Tech Stack

- **Language**: Go 1.25.3
- **Queue**: RabbitMQ
- **Database**: PostgreSQL (GORM)
- **Email**: AWS SES (LocalStack for local dev)

## Prerequisites

- Go 1.25+
- Node.js 22+ and npm 11+
- PostgreSQL (or use Docker Compose)
- RabbitMQ (or use Docker Compose)
- LocalStack (for local SES testing)

## Project Structure

```text
messaging-service/
├── cmd/
│   └── worker/           # Worker entrypoint
└── internal/
    ├── config/           # Configuration
    ├── email/            # SES email client
    ├── handler/          # Message handler
    └── repository/       # Data access layer
```

## Quick Start

### Using Docker Compose

```bash
# From infrastructure directory
docker-compose up -d messaging-service
```

### Local Development

1. Copy and configure environment file:

```bash
cp .env.example .env
# Edit .env with your local settings (DB credentials, RabbitMQ, etc.)
```

1. Start infrastructure (if not running):

```bash
# From infrastructure directory
docker-compose up -d postgres rabbitmq localstack flyway
```

1. Run the worker:

```bash
go run cmd/worker/main.go
```

## Available Commands

Using Task:

```bash
# Development
task run                 # Run the worker locally
task dev:install-tools   # Install dev tools

# Build and test
task build               # Build binary
task test                # Run tests
task test:coverage       # Run tests with coverage report
task clean               # Clean build artifacts

# Code quality
task format              # Format code with gofmt
task tidy                # Tidy and verify go.mod
task lint                # Run golangci-lint
task vet                 # Run go vet

# Security
task security:scan       # Run gosec security scanner
task security:vuln       # Check for vulnerabilities

# Docker
task docker:build        # Build Docker image
task docker:run          # Run service with docker-compose
task docker:stop         # Stop container
task docker:logs         # View container logs

# CI/CD
task ci:all              # Run all CI checks
```

Using Go directly:

```bash
go run cmd/worker/main.go                    # Run
go build -o bin/messaging-service cmd/worker/main.go  # Build
go test ./...                                 # Test
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ENVIRONMENT` | Environment type | `development` |
| `LOG_LEVEL` | Log level | `debug` |
| `LOG_FORMAT` | Log format (text/json) | `text` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_USER` | Database user | - |
| `DB_PASSWORD` | Database password | - |
| `DB_NAME` | Database name | `portfolio` |
| `DB_SSL_MODE` | PostgreSQL SSL mode | `disable` |
| `RABBITMQ_HOST` | RabbitMQ host | `localhost` |
| `RABBITMQ_PORT` | RabbitMQ port | `5672` |
| `RABBITMQ_USER` | RabbitMQ user | - |
| `RABBITMQ_PASSWORD` | RabbitMQ password | - |
| `RABBITMQ_EXCHANGE` | Exchange name | `contact_messages` |
| `RABBITMQ_QUEUE` | Queue name | `contact_messages` |
| `RABBITMQ_RETRY_DELAYS` | Retry delays | `1m,5m,30m,2h,12h` |
| `RABBITMQ_PREFETCH_COUNT` | Prefetch count | `1` |
| `RABBITMQ_CONSUMER_TAG` | Consumer tag | `messaging-service` |
| `AWS_REGION` | AWS region | `eu-north-1` |
| `SES_ENDPOINT` | SES endpoint (LocalStack only) | - |
| `AWS_ACCESS_KEY_ID` | AWS access key (LocalStack only) | - |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key (LocalStack only) | - |
| `SES_SENDER_EMAIL` | Sender email address | **required** |

### AWS Credentials

- **Local development**: Set `SES_ENDPOINT` to LocalStack URL and provide
  `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` (any value works with LocalStack)
- **Production**: Leave `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` empty.
  The SDK will automatically use IAM role credentials (EC2 instance profile,
  ECS task role, or IRSA for EKS)

## Message Flow

1. `messaging-api` publishes `ContactMessageEvent` to RabbitMQ
2. Worker consumes event from `contact_messages` queue
3. Fetches contact message details from PostgreSQL
4. Retrieves active recipients
5. Sends email to each recipient via SES
6. Records delivery attempt for idempotency
7. Updates message status (sent/failed)

On failure, message is retried with exponential backoff (configurable via
`RABBITMQ_RETRY_DELAYS`). After exhausting all retries, the message is moved to
a dead-letter queue for manual inspection.

## Integration

This service works with:

- **messaging-api**: Publishes contact message events
- **PostgreSQL**: Stores messages, recipients, and delivery attempts
- **RabbitMQ**: Message queue with retry support
- **AWS SES**: Email delivery (LocalStack for local development)

## License

[MIT](LICENSE)
