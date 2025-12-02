package options

import (
	"errors"

	"github.com/spf13/pflag"
	cliflag "k8s.io/component-base/cli/flag"
)

type StorageOptions struct {
	Enabled         bool
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Region          string
}

func NewStorageOptions(nfs *cliflag.NamedFlagSets) *StorageOptions {
	s := &StorageOptions{}
	return s.AddFlags(nfs.FlagSet("S3 Storage"))
}

func (s *StorageOptions) AddFlags(fs *pflag.FlagSet) *StorageOptions {
	fs.BoolVar(&s.Enabled, "enable-object-storage", s.Enabled, "Enable S3 compatible object storage for session recordings.")
	fs.StringVar(&s.Endpoint, "object-storage-endpoint", s.Endpoint, "S3 compatible endpoint (e.g., s3.amazonaws.com or minio.example.com:9000).")
	fs.StringVar(&s.AccessKeyID, "object-storage-access-key-id", s.AccessKeyID, "S3 access key ID.")
	fs.StringVar(&s.SecretAccessKey, "object-storage-secret-access-key", s.SecretAccessKey, "S3 secret access key.")
	fs.StringVar(&s.Bucket, "object-storage-bucket", s.Bucket, "S3 bucket for session recordings.")
	fs.StringVar(&s.Region, "object-storage-region", s.Region, "S3 region. If using AWS S3, this is required.")
	return s
}

func (s *StorageOptions) Validate() []error {
	if !s.Enabled {
		return nil
	}

	var errs []error
	if s.Endpoint == "" {
		errs = append(errs, errors.New("--s3-endpoint is required when --s3-enabled is true"))
	}
	if s.AccessKeyID == "" {
		errs = append(errs, errors.New("--s3-access-key-id is required when --s3-enabled is true"))
	}
	if s.SecretAccessKey == "" {
		errs = append(errs, errors.New("--s3-secret-access-key is required when --s3-enabled is true"))
	}
	if s.Bucket == "" {
		errs = append(errs, errors.New("--s3-bucket is required when --s3-enabled is true"))
	}

	return errs
}
