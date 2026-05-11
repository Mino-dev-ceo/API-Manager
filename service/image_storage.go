package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const defaultImageURLTTL = 7 * 24 * time.Hour

type ImageObject struct {
	Key         string `json:"key"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type imageStorage struct {
	bucket     string
	publicBase string
	client     *s3.Client
	presigner  *s3.PresignClient
}

var (
	ErrImageStorageNotConfigured = errors.New("R2 storage is not configured")

	imageStorageOnce sync.Once
	imageStorageInst *imageStorage
	imageStorageErr  error
)

func ImageTasksEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("IMAGE_TASKS_ENABLED"))
	if raw != "" {
		return strings.EqualFold(raw, "true") || raw == "1"
	}
	return strings.EqualFold(os.Getenv("IMAGE_STORAGE"), "r2") || strings.TrimSpace(os.Getenv("R2_BUCKET")) != ""
}

func getImageStorage() (*imageStorage, error) {
	imageStorageOnce.Do(func() {
		bucket := strings.TrimSpace(os.Getenv("R2_BUCKET"))
		endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("R2_ENDPOINT")), "/")
		region := strings.TrimSpace(os.Getenv("R2_REGION"))
		accessKey := strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID"))
		secretKey := strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY"))
		if region == "" {
			region = "auto"
		}
		if bucket == "" || endpoint == "" || accessKey == "" || secretKey == "" {
			imageStorageErr = ErrImageStorageNotConfigured
			return
		}

		cfg := aws.Config{
			Region:      region,
			Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		}
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
		imageStorageInst = &imageStorage{
			bucket:     bucket,
			publicBase: strings.TrimRight(strings.TrimSpace(os.Getenv("R2_PUBLIC_BASE_URL")), "/"),
			client:     client,
			presigner:  s3.NewPresignClient(client),
		}
	})
	return imageStorageInst, imageStorageErr
}

func PutImageObject(ctx context.Context, key string, contentType string, data []byte) (*ImageObject, error) {
	storage, err := getImageStorage()
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err = storage.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(storage.bucket),
		Key:          aws.String(key),
		Body:         bytes.NewReader(data),
		ContentType:  aws.String(contentType),
		CacheControl: aws.String("public, max-age=31536000, immutable"),
	})
	if err != nil {
		return nil, fmt.Errorf("upload image object to R2: %w", err)
	}
	objectURL, err := storage.objectURL(ctx, key)
	if err != nil {
		return nil, err
	}
	return &ImageObject{
		Key:         key,
		URL:         objectURL,
		ContentType: contentType,
		Size:        int64(len(data)),
	}, nil
}

func ImageObjectURL(ctx context.Context, key string) (string, error) {
	storage, err := getImageStorage()
	if err != nil {
		return "", err
	}
	return storage.objectURL(ctx, key)
}

func DeleteImageObject(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	storage, err := getImageStorage()
	if err != nil {
		return err
	}
	_, err = storage.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete image object from R2: %w", err)
	}
	return nil
}

func (s *imageStorage) objectURL(ctx context.Context, key string) (string, error) {
	if s.publicBase != "" {
		escaped := strings.ReplaceAll(url.PathEscape(key), "%2F", "/")
		return s.publicBase + "/" + escaped, nil
	}
	req, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = defaultImageURLTTL
	})
	if err != nil {
		return "", fmt.Errorf("sign image object URL: %w", err)
	}
	return req.URL, nil
}
