package config

import (
	"fmt"
	"strconv"

	"github.com/go-playground/validator/v10"

	common "github.com/GunarsK-portfolio/portfolio-common/config"
)

// Config holds all configuration for the messaging worker service
type Config struct {
	common.DatabaseConfig
	common.RabbitMQConfig
	SES        SESConfig
	HealthPort int
}

// Load loads all configuration from environment variables
func Load() *Config {
	healthPortStr := common.GetEnv("PORT", "8080")
	healthPort, err := strconv.Atoi(healthPortStr)
	if err != nil {
		panic(fmt.Sprintf("Invalid PORT: %v", err))
	}

	cfg := &Config{
		DatabaseConfig: common.NewDatabaseConfig(),
		RabbitMQConfig: common.NewRabbitMQConfig(),
		SES:            NewSESConfig(),
		HealthPort:     healthPort,
	}

	validate := validator.New()
	if err := validate.Struct(cfg.SES); err != nil {
		panic(fmt.Sprintf("Invalid SES configuration: %v", err))
	}

	// Validate credentials configuration
	hasAccessKey := cfg.SES.AccessKey != ""
	hasSecretKey := cfg.SES.SecretKey != ""
	hasEndpoint := cfg.SES.Endpoint != ""

	// Credentials must be provided as a pair (both or neither)
	if hasAccessKey != hasSecretKey {
		panic("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must both be provided or both be empty")
	}

	// LocalStack requires explicit credentials
	if hasEndpoint && !hasAccessKey {
		panic("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required when using SES_ENDPOINT (LocalStack)")
	}

	return cfg
}
