package s3

import (
	"bytes"
	"context"
	"io"
	"testing"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

// fakeS3 is an in-memory S3 keyed by object key. It records the SSE
// settings of the last PutObject so tests can assert encryption-at-rest
// is requested. No AWS credentials or network are involved.
type fakeS3 struct {
	objects map[string][]byte

	lastSSE    types.ServerSideEncryption
	lastKMSKey string

	// Validate-path knobs.
	policyPublic bool          // GetBucketPolicyStatus.IsPublic
	aclGrants    []types.Grant // GetBucketAcl.Grants
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[*in.Key] = data
	f.lastSSE = in.ServerSideEncryption
	if in.SSEKMSKeyId != nil {
		f.lastKMSKey = *in.SSEKMSKeyId
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data, ok := f.objects[*in.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (f *fakeS3) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if _, ok := f.objects[*in.Key]; !ok {
		return nil, &types.NotFound{}
	}
	return &s3.HeadObjectOutput{}, nil
}

func (f *fakeS3) GetBucketPolicyStatus(_ context.Context, _ *s3.GetBucketPolicyStatusInput, _ ...func(*s3.Options)) (*s3.GetBucketPolicyStatusOutput, error) {
	return &s3.GetBucketPolicyStatusOutput{
		PolicyStatus: &types.PolicyStatus{IsPublic: awsv2.Bool(f.policyPublic)},
	}, nil
}

func (f *fakeS3) GetBucketAcl(_ context.Context, _ *s3.GetBucketAclInput, _ ...func(*s3.Options)) (*s3.GetBucketAclOutput, error) {
	return &s3.GetBucketAclOutput{Grants: f.aclGrants}, nil
}

// newFakeAdapter builds an s3 adapter wired to the in-memory fake.
func newFakeAdapter(t *testing.T, fs *fakeS3, cfg map[string]any) *adapter {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{"bucket": "b", "key": "jitenv/blob"}
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ad := a.(*adapter)
	ad.newClient = func(context.Context) (api, error) { return fs, nil }
	return ad
}

func TestS3PushPullRoundTrip(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)

	// Pull before any push reports no remote state.
	if _, _, err := a.Pull(context.Background()); err != syncadapters.ErrNoRemoteState {
		t.Fatalf("expected ErrNoRemoteState, got %v", err)
	}

	want := []byte("ciphertext-bytes")
	meta := syncadapter.Meta{Hash: "abc123", SchemaVersion: 1}
	if err := a.Push(context.Background(), want, meta); err != nil {
		t.Fatalf("push: %v", err)
	}

	got, gotMeta, err := a.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("blob roundtrip mismatch: %q", got)
	}
	if gotMeta != meta {
		t.Fatalf("meta roundtrip mismatch: %+v", gotMeta)
	}

	// Default (no KMS key) must request SSE-S3 (AES256).
	if fs.lastSSE != types.ServerSideEncryptionAes256 {
		t.Fatalf("expected SSE AES256, got %q", fs.lastSSE)
	}
}

// TestS3PutRequestsKMSWhenConfigured asserts a configured kms_key_id flips
// PutObject to SSE-KMS with that key.
func TestS3PutRequestsKMSWhenConfigured(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, map[string]any{
		"bucket":     "b",
		"key":        "jitenv/blob",
		"kms_key_id": "alias/jitenv",
	})
	if err := a.Push(context.Background(), []byte("x"), syncadapter.Meta{SchemaVersion: 1}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if fs.lastSSE != types.ServerSideEncryptionAwsKms {
		t.Fatalf("expected SSE aws:kms, got %q", fs.lastSSE)
	}
	if fs.lastKMSKey != "alias/jitenv" {
		t.Fatalf("expected kms key alias/jitenv, got %q", fs.lastKMSKey)
	}
}

// TestS3PullMissingMetaIsNoRemoteState: a blob present but its meta
// sidecar absent is treated as no remote state (corrupt remote).
func TestS3PullMissingMetaIsNoRemoteState(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)
	// Plant only the blob, not the meta.
	fs.objects["jitenv/blob"] = []byte("blob-only")
	if _, _, err := a.Pull(context.Background()); err != syncadapters.ErrNoRemoteState {
		t.Fatalf("expected ErrNoRemoteState for missing meta, got %v", err)
	}
}

// TestS3ValidateRejectsPublicPolicy: a bucket whose policy status reports
// IsPublic must be rejected.
func TestS3ValidateRejectsPublicPolicy(t *testing.T) {
	fs := newFakeS3()
	fs.policyPublic = true
	a := newFakeAdapter(t, fs, nil)
	if err := a.Validate(context.Background()); err == nil {
		t.Fatal("expected Validate to reject a publicly-accessible bucket (policy status)")
	}
}

// TestS3ValidateRejectsPublicACL: a bucket whose ACL grants read to the
// AllUsers group must be rejected.
func TestS3ValidateRejectsPublicACL(t *testing.T) {
	fs := newFakeS3()
	fs.aclGrants = []types.Grant{{
		Grantee:    &types.Grantee{URI: awsv2.String(groupAllUsers)},
		Permission: types.PermissionRead,
	}}
	a := newFakeAdapter(t, fs, nil)
	if err := a.Validate(context.Background()); err == nil {
		t.Fatal("expected Validate to reject a bucket with a public ACL grant")
	}
}

// TestS3ValidateAcceptsPrivateBucket: a non-public bucket validates.
func TestS3ValidateAcceptsPrivateBucket(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)
	if err := a.Validate(context.Background()); err != nil {
		t.Fatalf("expected private bucket to validate, got %v", err)
	}
}

// TestS3NewRequiresBucketAndKey guards the required-param contract.
func TestS3NewRequiresBucketAndKey(t *testing.T) {
	if _, err := New(map[string]any{"key": "k"}); err == nil {
		t.Fatal("expected missing bucket to be rejected")
	}
	if _, err := New(map[string]any{"bucket": "b"}); err == nil {
		t.Fatal("expected missing key to be rejected")
	}
	if _, err := New(map[string]any{"bucket": "b", "key": "k", "access_key_id": "AKIA"}); err == nil {
		t.Fatal("expected access_key_id without secret_access_key to be rejected")
	}
}
