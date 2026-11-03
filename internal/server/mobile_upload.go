package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type MobileUploadSignRequest struct {
	ObjectKey string
	MediaType string
	SizeBytes int64
	SHA256    string
	Expires   time.Duration
}

type MobileUploadSignResult struct {
	ObjectKey string
	UploadURL string
	PublicURL string
	ExpiresAt time.Time
	Headers   map[string]string
}

type MobileUploadSigner interface {
	PresignUpload(ctx context.Context, req MobileUploadSignRequest) (MobileUploadSignResult, error)
}

type MobileS3UploadConfig struct {
	Endpoint      string
	Region        string
	Bucket        string
	AccessKey     string
	SecretKey     string
	SessionToken  string
	Prefix        string
	PublicBaseURL string
	UsePathStyle  bool
	PresignTTL    time.Duration
}

type S3MobileUploadSigner struct {
	cfg       MobileS3UploadConfig
	presigner *s3.PresignClient
}

func NewS3MobileUploadSigner(cfg MobileS3UploadConfig) (*S3MobileUploadSigner, error) {
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	if cfg.Bucket == "" {
		return nil, errors.New("mobile s3 bucket is required")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("mobile s3 access key and secret key are required")
	}
	var endpoint *string
	if strings.TrimSpace(cfg.Endpoint) != "" {
		endpoint = aws.String(strings.TrimSpace(cfg.Endpoint))
	}
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: endpoint,
		Credentials: credentials.NewStaticCredentialsProvider(
			strings.TrimSpace(cfg.AccessKey),
			strings.TrimSpace(cfg.SecretKey),
			strings.TrimSpace(cfg.SessionToken),
		),
		UsePathStyle: cfg.UsePathStyle,
	})
	return &S3MobileUploadSigner{cfg: cfg, presigner: s3.NewPresignClient(client)}, nil
}

func (s *S3MobileUploadSigner) PresignUpload(ctx context.Context, req MobileUploadSignRequest) (MobileUploadSignResult, error) {
	if s == nil || s.presigner == nil {
		return MobileUploadSignResult{}, errors.New("mobile s3 signer is not configured")
	}
	ttl := req.Expires
	if ttl <= 0 {
		ttl = s.cfg.PresignTTL
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	objectKey := mobileJoinObjectKey(s.cfg.Prefix, req.ObjectKey)
	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.cfg.Bucket),
		Key:           aws.String(objectKey),
		ContentLength: aws.Int64(req.SizeBytes),
	}
	if strings.TrimSpace(req.MediaType) != "" {
		input.ContentType = aws.String(strings.TrimSpace(req.MediaType))
	}
	if strings.TrimSpace(req.SHA256) != "" {
		// Preserve client-provided checksums as metadata because many mobile
		// clients send hex SHA-256 while S3's checksum header expects base64.
		input.Metadata = map[string]string{"sha256": strings.TrimSpace(req.SHA256)}
	}
	signed, err := s.presigner.PresignPutObject(ctx, input, func(options *s3.PresignOptions) {
		options.Expires = ttl
	})
	if err != nil {
		return MobileUploadSignResult{}, err
	}
	return MobileUploadSignResult{
		ObjectKey: objectKey,
		UploadURL: signed.URL,
		PublicURL: mobileAttachmentURL(s.cfg.PublicBaseURL, objectKey),
		ExpiresAt: time.Now().Add(ttl).UTC(),
		Headers:   mobileSignedHeaders(signed.SignedHeader),
	}, nil
}

func mobileSignedHeaders(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if strings.EqualFold(key, "host") || len(values) == 0 {
			continue
		}
		out[strings.ToLower(key)] = values[0]
	}
	return out
}

func mobileJoinObjectKey(prefix, objectKey string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	objectKey = strings.Trim(strings.TrimSpace(objectKey), "/")
	if prefix == "" {
		return objectKey
	}
	if objectKey == "" {
		return prefix
	}
	return prefix + "/" + objectKey
}
