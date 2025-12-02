package config

import (
	common "github.com/GunarsK-portfolio/portfolio-common/config"
)

// SESConfig holds AWS SES email configuration
type SESConfig struct {
	Region      string `validate:"required"`
	Endpoint    string // Optional: LocalStack endpoint for local development
	AccessKey   string // Optional: required for LocalStack, empty for AWS IAM role auth
	SecretKey   string // Optional: required for LocalStack, empty for AWS IAM role auth
	SenderEmail string `validate:"required,email"`
}

// NewSESConfig creates a new SESConfig from environment variables
func NewSESConfig() SESConfig {
	return SESConfig{
		Region:      common.GetEnv("AWS_REGION", "eu-north-1"),
		Endpoint:    common.GetEnv("SES_ENDPOINT", ""),
		AccessKey:   common.GetEnv("AWS_ACCESS_KEY_ID", ""),
		SecretKey:   common.GetEnv("AWS_SECRET_ACCESS_KEY", ""),
		SenderEmail: common.GetEnvRequired("SES_SENDER_EMAIL"),
	}
}

// IsLocalStack returns true if using LocalStack endpoint
func (c SESConfig) IsLocalStack() bool {
	return c.Endpoint != ""
}
