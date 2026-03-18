package main

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/GunarsK-portfolio/messaging-service/internal/config"
	"github.com/GunarsK-portfolio/messaging-service/internal/email"
	"github.com/GunarsK-portfolio/messaging-service/internal/handler"
	"github.com/GunarsK-portfolio/messaging-service/internal/repository"
	"github.com/GunarsK-portfolio/messaging-service/internal/routes"
	commondb "github.com/GunarsK-portfolio/portfolio-common/database"
	"github.com/GunarsK-portfolio/portfolio-common/health"
	"github.com/GunarsK-portfolio/portfolio-common/logger"
	"github.com/GunarsK-portfolio/portfolio-common/queue"
	"github.com/GunarsK-portfolio/portfolio-common/server"
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

	// Context for consumer shutdown (cancelled when server receives signal)
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel is called in RunWithCleanup shutdown callback

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
	appLogger.Info("Database connection established")

	// RabbitMQ publisher (needed for retry queue infrastructure)
	publisher, err := queue.NewRabbitMQPublisher(cfg.RabbitMQConfig)
	if err != nil {
		appLogger.Error("Failed to connect to RabbitMQ publisher", "error", err)
		os.Exit(1)
	}
	appLogger.Info("RabbitMQ publisher established")

	// RabbitMQ consumer
	consumer, err := queue.NewRabbitMQConsumer(cfg.RabbitMQConfig, publisher, appLogger)
	if err != nil {
		appLogger.Error("Failed to create RabbitMQ consumer", "error", err)
		os.Exit(1)
	}
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

	// Health checks
	healthAgg := health.NewAggregator(3 * time.Second)
	healthAgg.Register(health.NewPostgresChecker(db))
	healthAgg.Register(health.NewRabbitMQChecker(publisher.Connection()))

	// Start consumer in background
	go func() {
		appLogger.Info("Starting message consumer",
			"queue", cfg.Queue,
			"maxRetries", publisher.MaxRetries(),
		)

		if err := consumer.Consume(ctx, msgHandler.Process); err != nil {
			if ctx.Err() != nil {
				appLogger.Info("Consumer stopped due to shutdown")
			} else {
				appLogger.Error("Consumer error", "error", err)
			}
		}
	}()

	// Health HTTP server
	router := gin.New()
	router.Use(logger.Recovery(appLogger))
	routes.Setup(router, healthAgg)

	appLogger.Info("Messaging worker ready", "port", cfg.HealthPort, "environment", os.Getenv("ENVIRONMENT"))

	serverCfg := server.DefaultConfig(strconv.Itoa(cfg.HealthPort))
	if err := server.RunWithCleanup(router, serverCfg, appLogger, func() {
		cancel()
		if closeErr := consumer.Close(); closeErr != nil {
			appLogger.Error("Failed to close RabbitMQ consumer", "error", closeErr)
		}
		if closeErr := publisher.Close(); closeErr != nil {
			appLogger.Error("Failed to close RabbitMQ publisher", "error", closeErr)
		}
		if closeErr := commondb.CloseDB(db); closeErr != nil {
			appLogger.Error("Failed to close database", "error", closeErr)
		}
	}); err != nil {
		appLogger.Error("Server error", "error", err)
		os.Exit(1)
	}
}
