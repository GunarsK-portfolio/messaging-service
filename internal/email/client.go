package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// Client defines the interface for email sending
type Client interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

// SESClient implements Client using AWS SES
type SESClient struct {
	client    *ses.Client
	fromEmail string
}

// Compile-time interface assertion
var _ Client = (*SESClient)(nil)

// Config holds SES client configuration
type Config struct {
	Region    string
	Endpoint  string // Optional: LocalStack endpoint for local development
	AccessKey string // Optional: required for LocalStack
	SecretKey string // Optional: required for LocalStack
	FromEmail string
}

// NewSESClient creates a new SES email client
func NewSESClient(ctx context.Context, cfg Config) (*SESClient, error) {
	// Validate required configuration
	if cfg.Region == "" {
		return nil, fmt.Errorf("region is required")
	}
	if cfg.FromEmail == "" {
		return nil, fmt.Errorf("from email is required")
	}

	var opts []func(*config.LoadOptions) error
	opts = append(opts, config.WithRegion(cfg.Region))

	// Use static credentials if provided (LocalStack), otherwise use IAM role
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	var sesOpts []func(*ses.Options)
	if cfg.Endpoint != "" {
		sesOpts = append(sesOpts, func(o *ses.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}

	return &SESClient{
		client:    ses.NewFromConfig(awsCfg, sesOpts...),
		fromEmail: cfg.FromEmail,
	}, nil
}

// SendEmail sends an email via SES
func (c *SESClient) SendEmail(ctx context.Context, to, subject, body string) error {
	// Validate inputs for clearer error messages
	if to == "" {
		return fmt.Errorf("recipient email address is required")
	}
	if subject == "" {
		return fmt.Errorf("email subject is required")
	}

	input := &ses.SendEmailInput{
		Source: aws.String(c.fromEmail),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Message: &types.Message{
			Subject: &types.Content{
				Data:    aws.String(subject),
				Charset: aws.String("UTF-8"),
			},
			Body: &types.Body{
				Text: &types.Content{
					Data:    aws.String(body),
					Charset: aws.String("UTF-8"),
				},
			},
		},
	}

	_, err := c.client.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("send email to %s: %w", to, err)
	}

	return nil
}
