// Package s3 implements a config-sync adapter that stores the encrypted
// blob as an object in an Amazon S3 (or S3-compatible) bucket. It mirrors
// the file/ssh adapters: the blob lives at the configured key and the
// non-secret Meta JSON sits beside it at "<key>.meta.json".
//
// The remote only ever receives the opaque AEAD ciphertext handed to
// Push — the sync engine encrypts before calling the adapter, so the
// adapter treats the bytes as opaque. As defence in depth the adapter
// ALSO requests server-side encryption at rest on every PutObject
// (SSE-S3 by default, or SSE-KMS when a KMS key ID is configured) and
// Validate refuses a bucket that is publicly readable where that is
// detectable via the bucket ACL or public-access policy status.
//
// Credential loading mirrors the AWS Secrets Manager source
// (internal/sources/aws): static keys from config, otherwise the AWS
// default credential chain, with an optional endpoint override for
// S3-compatible stores / LocalStack.
package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

const typeName = "s3"

// Well-known canned-group grantee URIs. A bucket ACL that grants READ (or
// FULL_CONTROL) to either of these is effectively public, so Validate
// rejects it.
const (
	groupAllUsers           = "http://acs.amazonaws.com/groups/global/AllUsers"
	groupAuthenticatedUsers = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"
)

func init() {
	syncadapters.Register(typeName, New)
}

// api is the narrow seam of S3 operations the adapter uses. The real
// *s3.Client satisfies it; tests inject an in-memory fake (mirrors the
// ssh adapter's `runner` seam) so no AWS credentials are ever needed.
type api interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetBucketAcl(ctx context.Context, in *s3.GetBucketAclInput, opts ...func(*s3.Options)) (*s3.GetBucketAclOutput, error)
	GetBucketPolicyStatus(ctx context.Context, in *s3.GetBucketPolicyStatusInput, opts ...func(*s3.Options)) (*s3.GetBucketPolicyStatusOutput, error)
}

type adapter struct {
	bucket string
	key    string // object key for the blob; ".meta.json" appended for meta
	kmsKey string // optional SSE-KMS key ID; empty → SSE-S3 (AES256)

	// lazy client construction: cfg is loaded once on first use so New
	// stays cheap and offline (mirrors how the aws source builds its
	// client on demand).
	newClient func(ctx context.Context) (api, error)
	cli       api
}

// New constructs an s3 adapter. Required params: "bucket" and "key".
// Optional params:
//
//	region            — AWS region (else default chain / env).
//	access_key_id     — static credentials (secret_access_key required).
//	secret_access_key — paired with access_key_id (sensitive).
//	session_token     — optional STS session token (sensitive).
//	kms_key_id        — SSE-KMS key; when set, PutObject uses aws:kms,
//	                    otherwise SSE-S3 (AES256) is requested.
//	endpoint_override — S3-compatible endpoint (e.g. LocalStack/MinIO).
//	use_path_style    — force path-style addressing (needed by MinIO etc).
func New(cfg map[string]any) (syncadapter.Adapter, error) {
	bucket := asString(cfg["bucket"])
	key := asString(cfg["key"])
	if bucket == "" {
		return nil, errors.New("s3 adapter: \"bucket\" param is required")
	}
	if key == "" {
		return nil, errors.New("s3 adapter: \"key\" param is required")
	}

	region := asString(cfg["region"])
	accessKeyID := asString(cfg["access_key_id"])
	secretAccessKey := asString(cfg["secret_access_key"])
	sessionToken := asString(cfg["session_token"])
	kmsKey := asString(cfg["kms_key_id"])
	endpointOverride := asString(cfg["endpoint_override"])
	usePathStyle := asBool(cfg["use_path_style"])

	if accessKeyID != "" && secretAccessKey == "" {
		return nil, errors.New("s3 adapter: access_key_id is set but secret_access_key is empty")
	}

	a := &adapter{bucket: bucket, key: key, kmsKey: kmsKey}
	a.newClient = func(ctx context.Context) (api, error) {
		opts := []func(*config.LoadOptions) error{}
		if region != "" {
			opts = append(opts, config.WithRegion(region))
		}
		if accessKeyID != "" {
			opts = append(opts, config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, sessionToken),
			))
		}
		awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("s3 adapter: load aws config: %w", err)
		}
		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if endpointOverride != "" {
				o.BaseEndpoint = awsv2.String(endpointOverride)
			}
			if usePathStyle {
				o.UsePathStyle = true
			}
		}), nil
	}
	return a, nil
}

func (a *adapter) Name() string { return typeName }

func (a *adapter) metaKey() string { return a.key + ".meta.json" }

// client returns the cached S3 client, building it on first use.
func (a *adapter) client(ctx context.Context) (api, error) {
	if a.cli != nil {
		return a.cli, nil
	}
	cli, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	a.cli = cli
	return cli, nil
}

// Validate confirms the bucket is reachable and refuses a bucket that is
// publicly readable where that is detectable. It performs no write.
//
// Public-readability is checked two ways, neither of which is fatal if
// the caller lacks the read-policy permission (a 403 on the inspection
// call is treated as "not detectable", not "public"):
//
//   - GetBucketPolicyStatus.IsPublic — true means the bucket policy grants
//     public access.
//   - GetBucketAcl grants to the AllUsers / AuthenticatedUsers groups.
func (a *adapter) Validate(ctx context.Context) error {
	cli, err := a.client(ctx)
	if err != nil {
		return err
	}

	// Reachability: a HEAD on the (possibly absent) blob exercises
	// credentials + bucket existence without writing. A 404 on the object
	// is fine — the bucket is reachable; a missing bucket / bad creds
	// surfaces as a different error.
	if _, err := cli.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awsv2.String(a.bucket),
		Key:    awsv2.String(a.key),
	}); err != nil && !isNotFound(err) {
		return fmt.Errorf("s3 adapter: validate bucket %q: %w", a.bucket, err)
	}

	// Refuse a publicly-readable bucket via policy status.
	if ps, err := cli.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{
		Bucket: awsv2.String(a.bucket),
	}); err == nil {
		if ps.PolicyStatus != nil && ps.PolicyStatus.IsPublic != nil && *ps.PolicyStatus.IsPublic {
			return fmt.Errorf("s3 adapter: bucket %q is publicly accessible (policy status); refusing to sync encrypted config to a public bucket", a.bucket)
		}
	}
	// (A permission error on GetBucketPolicyStatus is intentionally
	// ignored: not all roles can read it, and the ACL check below plus
	// SSE-on-write keep the invariant.)

	// Refuse a bucket whose ACL grants read to a public group.
	if acl, err := cli.GetBucketAcl(ctx, &s3.GetBucketAclInput{
		Bucket: awsv2.String(a.bucket),
	}); err == nil {
		for _, g := range acl.Grants {
			if g.Grantee == nil || g.Grantee.URI == nil {
				continue
			}
			uri := *g.Grantee.URI
			if uri == groupAllUsers || uri == groupAuthenticatedUsers {
				return fmt.Errorf("s3 adapter: bucket %q ACL grants %s to %s; refusing to sync encrypted config to a public bucket", a.bucket, g.Permission, uri)
			}
		}
	}

	return nil
}

func (a *adapter) Push(ctx context.Context, blob []byte, meta syncadapter.Meta) error {
	if err := a.Validate(ctx); err != nil {
		return err
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := a.putObject(ctx, a.key, blob); err != nil {
		return fmt.Errorf("s3 adapter: put blob: %w", err)
	}
	if err := a.putObject(ctx, a.metaKey(), mb); err != nil {
		return fmt.Errorf("s3 adapter: put meta: %w", err)
	}
	return nil
}

// putObject writes one object with server-side encryption requested:
// SSE-KMS when a key ID is configured, otherwise SSE-S3 (AES256).
func (a *adapter) putObject(ctx context.Context, key string, data []byte) error {
	cli, err := a.client(ctx)
	if err != nil {
		return err
	}
	in := &s3.PutObjectInput{
		Bucket: awsv2.String(a.bucket),
		Key:    awsv2.String(key),
		Body:   bytes.NewReader(data),
	}
	if a.kmsKey != "" {
		in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
		in.SSEKMSKeyId = awsv2.String(a.kmsKey)
	} else {
		in.ServerSideEncryption = types.ServerSideEncryptionAes256
	}
	_, err = cli.PutObject(ctx, in)
	return err
}

func (a *adapter) Pull(ctx context.Context) ([]byte, syncadapter.Meta, error) {
	blob, err := a.getObject(ctx, a.key)
	if errors.Is(err, syncadapters.ErrNoRemoteState) {
		return nil, syncadapter.Meta{}, syncadapters.ErrNoRemoteState
	}
	if err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("s3 adapter: get blob: %w", err)
	}
	mb, err := a.getObject(ctx, a.metaKey())
	if errors.Is(err, syncadapters.ErrNoRemoteState) {
		// A blob without its meta sidecar is corrupt remote state; treat
		// as missing (mirrors the file/ssh adapters).
		return nil, syncadapter.Meta{}, syncadapters.ErrNoRemoteState
	}
	if err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("s3 adapter: get meta: %w", err)
	}
	var meta syncadapter.Meta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return nil, syncadapter.Meta{}, fmt.Errorf("s3 adapter: parse meta: %w", err)
	}
	return blob, meta, nil
}

// getObject reads one object's bytes. A missing object is reported as
// syncadapters.ErrNoRemoteState.
func (a *adapter) getObject(ctx context.Context, key string) ([]byte, error) {
	cli, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	out, err := cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awsv2.String(a.bucket),
		Key:    awsv2.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, syncadapters.ErrNoRemoteState
		}
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// isNotFound reports whether err is an S3 "object/key absent" condition.
// GetObject surfaces *types.NoSuchKey; HeadObject surfaces *types.NotFound
// (a bare 404 with no typed body). Both mean "no remote object yet".
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	// Some endpoints (and HeadObject) return an untyped API error whose
	// code is NotFound / NoSuchKey; match defensively.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey":
			return true
		}
	}
	return false
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}
