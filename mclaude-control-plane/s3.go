package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// awsV4Scheme is the AWS Signature V4 authorization header scheme identifier.
const awsV4Scheme = "AWS4-HMAC-SHA256"

// s3Config holds the S3-compatible storage configuration.
// Loaded from environment variables on startup.
type s3Config struct {
	Endpoint  string // e.g. "https://s3.amazonaws.com" or MinIO URL
	Bucket    string // Single bucket name
	AccessID  string // S3 access key ID (from S3_ACCESS_KEY_ID env var)
	AccessSig string // S3 signing credential (from S3_SECRET_ACCESS_KEY env var)
	Region    string // AWS region (default: "us-east-1" for non-AWS S3-compatible stores)
}

// loadS3Config reads S3 configuration from environment variables.
// Returns nil if S3 is not configured (imports/attachments unavailable in this mode).
func loadS3Config() *s3Config {
	endpoint := os.Getenv("S3_ENDPOINT")
	bucket := os.Getenv("S3_BUCKET")
	accessID := os.Getenv("S3_ACCESS_KEY_ID")
	accessSig := os.Getenv("S3_SECRET_ACCESS_KEY")
	if endpoint == "" || bucket == "" || accessID == "" || accessSig == "" {
		return nil
	}
	region := os.Getenv("S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return &s3Config{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Bucket:    bucket,
		AccessID:  accessID,
		AccessSig: accessSig,
		Region:    region,
	}
}

// maxAttachmentBytes is the default maximum attachment size (50 MB).
const maxAttachmentBytes int64 = 50 * 1024 * 1024

// maxImportBytes is the default maximum import archive size (500 MB).
const maxImportBytes int64 = 500 * 1024 * 1024

// presignPutURL generates an AWS V4 pre-signed PUT URL for uploading to S3.
// The URL authorizes the holder to PUT an object at the given key for expirySeconds.
func (cfg *s3Config) presignPutURL(key string, expirySeconds int64) (string, error) {
	return cfg.presignURL(http.MethodPut, key, expirySeconds)
}

// presignGetURL generates an AWS V4 pre-signed GET URL for downloading from S3.
func (cfg *s3Config) presignGetURL(key string, expirySeconds int64) (string, error) {
	return cfg.presignURL(http.MethodGet, key, expirySeconds)
}

// presignURL generates an AWS V4 pre-signed URL for the given HTTP method and S3 key.
// Implements https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
func (cfg *s3Config) presignURL(method, key string, expirySeconds int64) (string, error) {
	now := time.Now().UTC()
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")

	// Determine host from endpoint.
	endpointURL, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return "", fmt.Errorf("parse S3 endpoint: %w", err)
	}
	// For path-style URLs (MinIO), the host is the endpoint host.
	// For virtual-hosted-style (AWS), host would be bucket.s3.amazonaws.com.
	// We use path-style for compatibility with MinIO.
	host := endpointURL.Host

	// Credential scope.
	credentialScope := date + "/" + cfg.Region + "/s3/aws4_request"
	credential := cfg.AccessID + "/" + credentialScope

	// Canonical URI — path-style: /{bucket}/{key}
	canonicalURI := "/" + cfg.Bucket + "/" + strings.TrimPrefix(key, "/")

	// Build canonical query string (must be sorted alphabetically).
	queryParams := url.Values{
		"X-Amz-Algorithm":     {"AWS4-HMAC-SHA256"},
		"X-Amz-Credential":    {credential},
		"X-Amz-Date":          {datetime},
		"X-Amz-Expires":       {fmt.Sprintf("%d", expirySeconds)},
		"X-Amz-SignedHeaders": {"host"},
	}
	canonicalQueryString := queryParams.Encode()

	// Canonical headers — only Host for pre-signed URLs.
	canonicalHeaders := "host:" + host + "\n"
	signedHeaders := "host"

	// Payload hash is UNSIGNED-PAYLOAD for pre-signed URLs.
	payloadHash := "UNSIGNED-PAYLOAD"

	// Canonical request.
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign.
	hashedCanonicalRequest := sha256Hex([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + datetime + "\n" + credentialScope + "\n" + hashedCanonicalRequest

	// Derive the AWS V4 HMAC chain using the four-stage derivation.
	// Each stage mixes a different scope component into the HMAC chain.
	hmac1 := hmacSHA256([]byte("AWS4"+cfg.AccessSig), []byte(date))
	hmac2 := hmacSHA256(hmac1, []byte(cfg.Region))
	hmac3 := hmacSHA256(hmac2, []byte("s3"))
	hmac4 := hmacSHA256(hmac3, []byte("aws4_request"))

	// Signature.
	signature := hex.EncodeToString(hmacSHA256(hmac4, []byte(stringToSign)))

	// Build the final URL.
	presignedURL := cfg.Endpoint + canonicalURI + "?" + canonicalQueryString + "&X-Amz-Signature=" + signature
	return presignedURL, nil
}

// s3ObjectExists checks if an S3 object exists using a HEAD request with a pre-signed URL.
// Returns true if the object exists and is accessible.
func (cfg *s3Config) s3ObjectExists(key string) (bool, error) {
	signedURL, err := cfg.presignURL(http.MethodHead, key, 60)
	if err != nil {
		return false, fmt.Errorf("presign HEAD URL: %w", err)
	}
	resp, err := http.Head(signedURL) //nolint:noctx
	if err != nil {
		return false, fmt.Errorf("HEAD S3 object: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return false, nil
	}
	return false, fmt.Errorf("HEAD S3 object status: %d", resp.StatusCode)
}

// s3DeleteObject deletes an S3 object using a pre-signed DELETE request.
func (cfg *s3Config) s3DeleteObject(key string) error {
	signedURL, err := cfg.presignURL(http.MethodDelete, key, 60)
	if err != nil {
		return fmt.Errorf("presign DELETE URL: %w", err)
	}
	req, err := http.NewRequest(http.MethodDelete, signedURL, nil) //nolint:noctx
	if err != nil {
		return fmt.Errorf("create DELETE request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE S3 object: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("DELETE S3 object status: %d", resp.StatusCode)
}

// s3DeletePrefix deletes all S3 objects whose key starts with the given prefix.
// Used for project deletion to clean up import archives and attachments (ADR-0053).
// The cleanup is best-effort: the caller should log and ignore any returned error.
func (cfg *s3Config) s3DeletePrefix(prefix string) error {
	keys, err := cfg.s3ListObjectKeys(prefix)
	if err != nil {
		return fmt.Errorf("list S3 objects for prefix %q: %w", prefix, err)
	}
	var lastErr error
	for _, key := range keys {
		if delErr := cfg.s3DeleteObject(key); delErr != nil {
			// Record the error but continue deleting remaining objects.
			lastErr = delErr
		}
	}
	return lastErr
}

// s3ListObjectKeys returns all object keys under the given prefix.
// Uses S3 ListObjectsV2 with automatic pagination.
func (cfg *s3Config) s3ListObjectKeys(prefix string) ([]string, error) {
	var allKeys []string
	continuationToken := ""
	for {
		keys, nextToken, err := cfg.s3ListObjectKeysPage(prefix, continuationToken)
		if err != nil {
			return nil, err
		}
		allKeys = append(allKeys, keys...)
		if nextToken == "" {
			break
		}
		continuationToken = nextToken
	}
	return allKeys, nil
}

// s3ListObjectKeysPage lists one page of object keys under the given prefix using
// the S3 ListObjectsV2 API with AWS V4 Authorization-header signing.
// Returns (keys, nextContinuationToken, error); nextToken is empty when no more pages.
func (cfg *s3Config) s3ListObjectKeysPage(prefix, continuationToken string) (keys []string, nextToken string, err error) {
	now := time.Now().UTC()
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")

	endpointURL, parseErr := url.Parse(cfg.Endpoint)
	if parseErr != nil {
		return nil, "", fmt.Errorf("parse S3 endpoint: %w", parseErr)
	}
	host := endpointURL.Host

	// Build and sort query parameters (url.Values.Encode sorts alphabetically).
	queryVals := url.Values{
		"list-type": {"2"},
		"max-keys":  {"1000"},
		"prefix":    {prefix},
	}
	if continuationToken != "" {
		queryVals.Set("continuation-token", continuationToken)
	}
	canonicalQueryString := queryVals.Encode()

	canonicalURI := "/" + cfg.Bucket
	credentialScope := date + "/" + cfg.Region + "/s3/aws4_request"
	canonicalHeaders := "host:" + host + "\nx-amz-date:" + datetime + "\n"
	signedHeaders := "host;x-amz-date"
	payloadHash := sha256Hex([]byte(""))

	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	hashedCanonicalRequest := sha256Hex([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + datetime + "\n" + credentialScope + "\n" + hashedCanonicalRequest

	hmac1 := hmacSHA256([]byte("AWS4"+cfg.AccessSig), []byte(date))
	hmac2 := hmacSHA256(hmac1, []byte(cfg.Region))
	hmac3 := hmacSHA256(hmac2, []byte("s3"))
	hmac4 := hmacSHA256(hmac3, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(hmac4, []byte(stringToSign)))

	authorization := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		awsV4Scheme, cfg.AccessID, credentialScope, signedHeaders, signature)

	reqURL := cfg.Endpoint + canonicalURI + "?" + canonicalQueryString
	req, reqErr := http.NewRequest(http.MethodGet, reqURL, nil) //nolint:noctx
	if reqErr != nil {
		return nil, "", fmt.Errorf("create list request: %w", reqErr)
	}
	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Date", datetime)
	req.Header.Set("Authorization", authorization)

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return nil, "", fmt.Errorf("list S3 objects: %w", doErr)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("list S3 objects status: %d", resp.StatusCode)
	}

	// Parse the XML response using token-based parsing.
	// This handles both namespaced responses (AWS S3 uses
	// xmlns="http://s3.amazonaws.com/doc/2006-03-01/") and non-namespaced responses
	// (MinIO dev mode, test servers) by matching on local element names only.
	dec := xml.NewDecoder(resp.Body)
	var (
		inContents bool
		inKey      bool
		inTrunc    bool
		inToken    bool
		isTrunc    bool
		nt         string
	)
	for {
		tok, tokErr := dec.Token()
		if tokErr == io.EOF {
			break
		}
		if tokErr != nil {
			return nil, "", fmt.Errorf("parse list response: %w", tokErr)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Contents":
				inContents = true
			case "Key":
				if inContents {
					inKey = true
				}
			case "IsTruncated":
				inTrunc = true
			case "NextContinuationToken":
				inToken = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "Contents":
				inContents = false
				inKey = false
			case "Key":
				inKey = false
			case "IsTruncated":
				inTrunc = false
			case "NextContinuationToken":
				inToken = false
			}
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			switch {
			case inKey:
				if text != "" {
					keys = append(keys, text)
				}
			case inTrunc:
				isTrunc = isTrunc || text == "true"
			case inToken:
				nt = text
			}
		}
	}

	if isTrunc {
		nextToken = nt
	}
	return keys, nextToken, nil
}

// hmacSHA256 computes HMAC-SHA256 of data with key.
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// sha256Hex returns the lowercase hex SHA-256 hash of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
