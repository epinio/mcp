package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client wraps an aws-sdk-go-v2 S3 client configured for the Epinio
// s3-gateway. Connection details come from the mounted Epinio configuration
// bound to this pod (typically "epinio-s3-gateway"), with env var fallbacks
// for local development.
type S3Client struct {
	API    *s3.Client
	Bucket string
	Config S3Config
}

// S3Config captures the settings needed to talk to the gateway.
type S3Config struct {
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
}

var (
	s3Once   sync.Once
	s3Client *S3Client
	s3Err    error
)

// ConfigDir resolves the Epinio configuration mount dir. Bound configurations
// are mounted at /configurations/<name>/ by default in the standard appchart.
const defaultS3ConfigMount = "/configurations/epinio-s3-gateway"

// GetS3Client returns a lazily initialized S3 client pointed at Epinio's
// s3-gateway. First-call configuration precedence: S3_ENDPOINT env var >
// mounted configuration files under $S3_CONFIG_PATH (default
// /configurations/epinio-s3-gateway). Returns the same error on repeated
// calls when no configuration is available.
func GetS3Client(ctx context.Context) (*S3Client, error) {
	s3Once.Do(func() {
		cfg, err := loadS3Config()
		if err != nil {
			s3Err = err
			return
		}
		protocol := "http"
		if cfg.UseSSL {
			protocol = "https"
		}
		endpoint := fmt.Sprintf("%s://%s", protocol, cfg.Endpoint)

		ak, sk := cfg.AccessKeyID, cfg.SecretAccessKey
		if ak == "" {
			// SeaweedFS allows anonymous access when s3.enableAuth is false,
			// but the SDK refuses to build a client without some credentials,
			// so we supply placeholder strings.
			ak, sk = "anonymous", "anonymous"
		}

		api := s3.New(s3.Options{
			Region:       "us-east-1", // SeaweedFS ignores region but the SDK requires one
			BaseEndpoint: aws.String(endpoint),
			Credentials:  credentials.NewStaticCredentialsProvider(ak, sk, ""),
			UsePathStyle: true, // SeaweedFS doesn't do virtual-hosted-style
		})
		s3Client = &S3Client{API: api, Bucket: cfg.Bucket, Config: cfg}
	})
	return s3Client, s3Err
}

func loadS3Config() (S3Config, error) {
	// Env var override path (local dev / tests)
	if ep := os.Getenv("S3_ENDPOINT"); ep != "" {
		return S3Config{
			Endpoint:        ep,
			Bucket:          envOr("S3_BUCKET", "epinio"),
			AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
			UseSSL:          os.Getenv("S3_USE_SSL") == "true",
		}, nil
	}

	dir := envOr("S3_CONFIG_PATH", defaultS3ConfigMount)
	endpoint, err := readConfigFile(dir, "endpoint")
	if err != nil {
		return S3Config{}, fmt.Errorf(
			"s3 not configured: no S3_ENDPOINT env var and no configuration mounted at %s: %w",
			dir, err,
		)
	}
	bucket, _ := readConfigFile(dir, "bucket")
	if bucket == "" {
		bucket = "epinio"
	}
	useSSLRaw, _ := readConfigFile(dir, "useSSL")
	useSSL := strings.EqualFold(useSSLRaw, "true")

	// Credentials file is optional — gateway may have auth disabled.
	credsRaw, _ := readConfigFile(dir, "credentials")
	ak, sk := ParseAWSCredentials(credsRaw)

	return S3Config{
		Endpoint:        endpoint,
		Bucket:          bucket,
		AccessKeyID:     ak,
		SecretAccessKey: sk,
		UseSSL:          useSSL,
	}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func readConfigFile(dir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// ParseAWSCredentials reads an INI-style AWS CLI credentials blob and returns
// the [default] profile's access key and secret. Returns empty strings if the
// blob is empty or malformed — callers treat that as "anonymous".
func ParseAWSCredentials(raw string) (accessKeyID, secretAccessKey string) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if eq := strings.IndexByte(line, '='); eq >= 0 {
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			switch key {
			case "aws_access_key_id":
				accessKeyID = val
			case "aws_secret_access_key":
				secretAccessKey = val
			}
		}
	}
	return
}

// GetObject fetches an object from the configured bucket and returns its
// body as a byte slice. Intended for small/medium blobs — staging tarballs
// typically fit in a few MB. For very large objects, consider streaming
// directly to the caller instead.
func (c *S3Client) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	resp, err := c.API.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("get object %q: %w", key, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read object %q: %w", key, err)
	}
	ct := ""
	if resp.ContentType != nil {
		ct = *resp.ContentType
	}
	return body, ct, nil
}
