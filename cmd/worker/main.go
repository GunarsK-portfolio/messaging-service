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
		Level:       os.Getenv("LOG_LEVEL"),
		Format:      os.Getenv("LOG_FORMAT"),
		ServiceName: "messaging-service",
		AddSource:   os.Getenv("LOG_SOURCE") == "true",
	})

	appLogger.Info("Starting messaging worker", "version", "1.0")

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database connection
	//nolint:staticcheck // Embedded field name required due to ambiguous fields
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

	// RabbitMQ publisher (needed for retry queue infrastructure)
	publisher, err := queue.NewRabbitMQPublisher(cfg.RabbitMQConfig)
	if err != nil {
		appLogger.Error("Failed to connect to RabbitMQ publisher", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := publisher.Close(); closeErr != nil {
			appLogger.Error("Failed to close RabbitMQ publisher", "error", closeErr)
		}
	}()
	appLogger.Info("RabbitMQ publisher established")

	// RabbitMQ consumer
	consumer, err := queue.NewRabbitMQConsumer(cfg.RabbitMQConfig, publisher, appLogger)
	if err != nil {
		appLogger.Error("Failed to create RabbitMQ consumer", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := consumer.Close(); closeErr != nil {
			appLogger.Error("Failed to close RabbitMQ consumer", "error", closeErr)
		}
	}()
	appLogger.Info("RabbitMQ consumer established")

	// SES email client
	emailClient, err := email.NewSESClient(ctx, email.Config{
		Region:    cfg.SES.Region,
		Endpoint:  cfg.SES.Endpoint,
		AccessKey: cfg.SES.AccessKey,
		SecretKey: cfg.SES.SecretKey,
		FromEmail: cfg.SES.SenderEmail,
	})
	if err != nil {
		appLogger.Error("Failed to create SES client", "error", err)
		os.Exit(1)
	}
	appLogger.Info("SES email client created")

	// Repository and handler
	repo := repository.New(db)
	msgHandler := handler.New(repo, emailClient, appLogger)

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		appLogger.Info("Received shutdown signal", "signal", sig)
		cancel()
	}()

	// Start consuming messages
	appLogger.Info("Starting message consumer",
		"queue", cfg.Queue,
		"maxRetries", publisher.MaxRetries(),
	)

	if err := consumer.Consume(ctx, msgHandler.Process); err != nil {
		if ctx.Err() != nil {
			appLogger.Info("Consumer stopped due to shutdown")
		} else {
			appLogger.Error("Consumer error", "error", err)
			os.Exit(1)
		}
	}

	appLogger.Info("Messaging worker shutdown complete")
}
