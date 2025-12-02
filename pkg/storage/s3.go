package storage

import (
	"context"
	"fmt"
	"os"

	"github.com/Improwised/kube-oidc-proxy/cmd/app/options"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Uploader struct {
	Client *s3.Client
	Bucket string
}

func NewS3Uploader(opts *options.StorageOptions) (*S3Uploader, error) {
	if !opts.Enabled {
		return nil, nil
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(opts.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
			o.UsePathStyle = true
		}
	})

	return &S3Uploader{
		Client: client,
		Bucket: opts.Bucket,
	}, nil
}

func (u *S3Uploader) Upload(filePath, objectKey string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	_, err = u.Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(u.Bucket),
		Key:    aws.String(objectKey),
		Body:   file,
	})

	return err
}
