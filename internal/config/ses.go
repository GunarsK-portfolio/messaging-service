package config

import (
	common "github.com/GunarsK-portfolio/portfolio-common/config"
)

// SESConfig holds AWS SES email configuration.
//
// Credential handling:
//   - LocalStack (development): Set SES_ENDPOINT and provide AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY
//   - AWS (production): Leave credentials empty to use IAM role-based authentication
//     (EC2 instance profile, ECS task role, or IRSA for EKS)
//
// Never commit real AWS credentials. Use environment variables or secrets manager.
type SESConfig struct {
	Region      string `validate:"required"`
	Endpoint    string // Optional: LocalStack endpoint (e.g., http://localhost:4566) for local dev
	AccessKey   string // Optional: AWS_ACCESS_KEY_ID - required for LocalStack, omit for IAM roles
	SecretKey   string // Optional: AWS_SECRET_ACCESS_KEY - required for LocalStack, omit for IAM roles
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
