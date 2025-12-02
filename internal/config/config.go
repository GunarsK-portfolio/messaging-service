package config

import (
	"fmt"

	"github.com/go-playground/validator/v10"

	common "github.com/GunarsK-portfolio/portfolio-common/config"
)

// Config holds all configuration for the messaging worker service
type Config struct {
	common.DatabaseConfig
	common.RabbitMQConfig
	SES SESConfig
}

// Load loads all configuration from environment variables
func Load() *Config {
	cfg := &Config{
		DatabaseConfig: common.NewDatabaseConfig(),
		RabbitMQConfig: common.NewRabbitMQConfig(),
		SES:            NewSESConfig(),
	}

	validate := validator.New()
	if err := validate.Struct(cfg.SES); err != nil {
		panic(fmt.Sprintf("Invalid SES configuration: %v", err))
	}

	// Validate credentials are provided as a pair (both or neither)
	hasAccessKey := cfg.SES.AccessKey != ""
	hasSecretKey := cfg.SES.SecretKey != ""
	if hasAccessKey != hasSecretKey {
		panic("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must both be provided or both be empty")
	}

	return cfg
}
