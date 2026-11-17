// Package storage provides the resolved S3 connection; it contains no provider resolver.
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/config"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/retainer"
)

type Client interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
	GetObjectLockConfiguration(context.Context, *s3.GetObjectLockConfigurationInput, ...func(*s3.Options)) (*s3.GetObjectLockConfigurationOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}
type Store struct {
	Client         Client
	Bucket, Prefix string
}

func New(ctx context.Context, c config.Config) (*Store, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(c.Region), awsconfig.WithRetryMaxAttempts(3), awsconfig.WithClientLogMode(0)}
	if c.AccessKeyFile != "" {
		read := func(path string) (string, error) {
			b, e := os.ReadFile(path)
			if e != nil {
				return "", fmt.Errorf("cannot read credential file")
			}
			v := strings.TrimSpace(string(b))
			if v == "" {
				return "", fmt.Errorf("empty credential file")
			}
			return v, nil
		}
		access, e := read(c.AccessKeyFile)
		if e != nil {
			return nil, e
		}
		secret, e := read(c.SecretKeyFile)
		if e != nil {
			return nil, e
		}
		token := ""
		if c.SessionTokenFile != "" {
			token, e = read(c.SessionTokenFile)
			if e != nil {
				return nil, e
			}
		}
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, token)))
	} else {
		a, s, t := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")
		if a != "" || s != "" || t != "" {
			if a == "" || s == "" {
				return nil, fmt.Errorf("incomplete environment credentials")
			}
			opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(a, s, t)))
		}
	}
	if c.CAFile != "" {
		f, e := os.Open(c.CAFile)
		if e != nil {
			return nil, fmt.Errorf("cannot open CA bundle")
		}
		defer f.Close()
		opts = append(opts, awsconfig.WithCustomCABundle(f))
	}
	cfg, e := awsconfig.LoadDefaultConfig(ctx, opts...)
	if e != nil {
		return nil, fmt.Errorf("S3 configuration failed")
	}
	// Resolve once before requests: an explicit or selected provider failure must
	// never fall through to probing another credential source.
	if _, e = cfg.Credentials.Retrieve(ctx); e != nil {
		return nil, fmt.Errorf("credential resolution failed")
	}
	cl := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = c.PathStyle
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	return &Store{Client: cl, Bucket: c.Bucket, Prefix: c.Prefix}, nil
}
func (s *Store) List(ctx context.Context) ([]retainer.Object, error) {
	objects := []retainer.Object{}
	token := ""
	seen := map[string]bool{}
	for {
		in := &s3.ListObjectsV2Input{Bucket: &s.Bucket, Prefix: &s.Prefix}
		if token != "" {
			in.ContinuationToken = &token
		}
		page, e := s.Client.ListObjectsV2(ctx, in)
		if e != nil {
			return nil, e
		}
		for _, o := range page.Contents {
			if o.Key == nil || o.Size == nil {
				return nil, fmt.Errorf("incomplete object metadata")
			}
			objects = append(objects, retainer.Object{Key: *o.Key, Size: *o.Size})
		}
		if !aws.ToBool(page.IsTruncated) {
			return objects, nil
		}
		token = aws.ToString(page.NextContinuationToken)
		if token == "" || seen[token] {
			return nil, fmt.Errorf("invalid continuation token")
		}
		seen[token] = true
	}
}
func (s *Store) Head(ctx context.Context, key string) (retainer.Object, error) {
	h, e := s.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.Bucket, Key: &key})
	if e != nil {
		return retainer.Object{}, e
	}
	if h.ContentLength == nil || aws.ToBool(h.DeleteMarker) {
		return retainer.Object{}, fmt.Errorf("inconsistent upload metadata")
	}
	return retainer.Object{Key: key, Size: *h.ContentLength}, nil
}
func (s *Store) Protection(ctx context.Context) (string, error) {
	v, e := s.Client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &s.Bucket})
	if e != nil {
		return "unknown", e
	}
	if v.Status != "" || v.MFADelete != "" {
		return "versioned_or_mfa_delete", fmt.Errorf("versioning unsupported")
	}
	l, e := s.Client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: &s.Bucket})
	if e != nil {
		var api smithy.APIError
		if errors.As(e, &api) && api.ErrorCode() == "ObjectLockConfigurationNotFoundError" {
			return "unversioned_unlocked", nil
		}
		return "unknown", e
	}
	if l.ObjectLockConfiguration == nil {
		return "unknown", fmt.Errorf("missing lock configuration")
	}
	// Any configured lock, including an unknown future enum, is unsupported.
	return "object_lock_configured", fmt.Errorf("object lock unsupported")
}
func (s *Store) Delete(ctx context.Context, key string) error {
	out, e := s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.Bucket, Key: &key})
	if e != nil {
		return e
	}
	if aws.ToBool(out.DeleteMarker) || out.VersionId != nil {
		return fmt.Errorf("unexpected versioned deletion")
	}
	return nil
}
