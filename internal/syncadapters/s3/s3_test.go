package s3

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

// fakeS3 is an in-memory S3 keyed by object key. It records the SSE
// settings of the last PutObject so tests can assert encryption-at-rest
// is requested. No AWS credentials or network are involved.
//
// It also implements minimal ETag + If-Match / If-None-Match semantics
// so the CAS path (#278) can be exercised without LocalStack.
type fakeS3 struct {
	objects map[string][]byte
	etags   map[string]string

	lastSSE    types.ServerSideEncryption
	lastKMSKey string
	// putHeaders records the precondition headers seen on EACH PutObject,
	// keyed by object key. Tests assert per-key (e.g. the BLOB put got
	// IfMatch, the META put did not).
	putHeaders map[string]putHeaders

	// Validate-path knobs.
	policyPublic bool          // GetBucketPolicyStatus.IsPublic
	aclGrants    []types.Grant // GetBucketAcl.Grants
}

type putHeaders struct {
	IfMatch     string
	IfNoneMatch string
}

func newFakeS3() *fakeS3 {
	return &fakeS3{
		objects:    map[string][]byte{},
		etags:      map[string]string{},
		putHeaders: map[string]putHeaders{},
	}
}

// fakePreconditionFailed is an APIError fake matching the smithy
// interface so the adapter's isPreconditionFailed detector triggers.
type fakePreconditionFailed struct{}

func (fakePreconditionFailed) Error() string                 { return "PreconditionFailed" }
func (fakePreconditionFailed) ErrorCode() string             { return "PreconditionFailed" }
func (fakePreconditionFailed) ErrorMessage() string          { return "PreconditionFailed" }
func (fakePreconditionFailed) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	hdr := putHeaders{}
	if in.IfMatch != nil {
		hdr.IfMatch = *in.IfMatch
	}
	if in.IfNoneMatch != nil {
		hdr.IfNoneMatch = *in.IfNoneMatch
	}
	f.putHeaders[*in.Key] = hdr
	// Evaluate preconditions before the write lands.
	if in.IfMatch != nil {
		cur, exists := f.etags[*in.Key]
		if !exists || cur != *in.IfMatch {
			return nil, fakePreconditionFailed{}
		}
	}
	if in.IfNoneMatch != nil && *in.IfNoneMatch == "*" {
		if _, exists := f.objects[*in.Key]; exists {
			return nil, fakePreconditionFailed{}
		}
	}
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[*in.Key] = data
	// S3 ETags for non-multipart uploads are the hex MD5 in quotes.
	sum := md5.Sum(data)
	etag := fmt.Sprintf("\"%s\"", hex.EncodeToString(sum[:]))
	f.etags[*in.Key] = etag
	f.lastSSE = in.ServerSideEncryption
	if in.SSEKMSKeyId != nil {
		f.lastKMSKey = *in.SSEKMSKeyId
	}
	return &s3.PutObjectOutput{ETag: awsv2.String(etag)}, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data, ok := f.objects[*in.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	etag := f.etags[*in.Key]
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data)), ETag: awsv2.String(etag)}, nil
}

func (f *fakeS3) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if _, ok := f.objects[*in.Key]; !ok {
		return nil, &types.NotFound{}
	}
	etag := f.etags[*in.Key]
	return &s3.HeadObjectOutput{ETag: awsv2.String(etag)}, nil
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
	// First push: no prior observation, so the BLOB put must send
	// IfNoneMatch: "*" to guard against a concurrent first writer (#278).
	if got := fs.putHeaders["jitenv/blob"].IfNoneMatch; got != "*" {
		t.Fatalf("expected IfNoneMatch=\"*\" on first blob push, got %q", got)
	}
	// The meta sidecar must NOT carry a precondition: its CAS is
	// implicit in the blob's (a stale-blob push never reaches the meta).
	if got := fs.putHeaders["jitenv/blob.meta.json"]; got.IfMatch != "" || got.IfNoneMatch != "" {
		t.Fatalf("expected meta put to be unconditional, got %+v", got)
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

	// A second push after that Pull must carry IfMatch=<observed ETag>
	// on the BLOB put (CAS against the known prior version).
	if err := a.Push(context.Background(), []byte("next"), syncadapter.Meta{Hash: "x", SchemaVersion: 1}); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if fs.putHeaders["jitenv/blob"].IfMatch == "" {
		t.Fatalf("expected IfMatch header on Push-after-Pull, got none")
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

// TestS3PullMissingMetaIsIncomplete: a blob present but its meta
// sidecar absent must surface ErrRemoteStateIncomplete so the engine's
// pre-push fence refuses a non-force overwrite of the orphan (#279).
func TestS3PullMissingMetaIsIncomplete(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)
	fs.objects["jitenv/blob"] = []byte("blob-only")
	fs.etags["jitenv/blob"] = "\"deadbeef\""
	_, _, err := a.Pull(context.Background())
	if !errors.Is(err, syncadapters.ErrRemoteStateIncomplete) {
		t.Fatalf("expected ErrRemoteStateIncomplete for missing meta, got %v", err)
	}
}

// TestS3PullMissingBlobIsIncomplete: the symmetric case — meta
// present, blob absent — must also surface ErrRemoteStateIncomplete.
func TestS3PullMissingBlobIsIncomplete(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)
	fs.objects["jitenv/blob.meta.json"] = []byte(`{"hash":"abc","schema_version":1}`)
	fs.etags["jitenv/blob.meta.json"] = "\"feedface\""
	_, _, err := a.Pull(context.Background())
	if !errors.Is(err, syncadapters.ErrRemoteStateIncomplete) {
		t.Fatalf("expected ErrRemoteStateIncomplete for missing blob, got %v", err)
	}
}

// TestS3PushCASRejectsConcurrentClobber: machine A pulls, then a third
// party rewrites the blob (changing its ETag), then A pushes — the
// PutObject must fail with ErrPreconditionFailed instead of silently
// overwriting B's update (#278).
func TestS3PushCASRejectsConcurrentClobber(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)

	// Seed an initial (blob, meta) pair so Pull sees a real ETag.
	if err := a.Push(context.Background(), []byte("v1"), syncadapter.Meta{Hash: "h1", SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Simulate a concurrent writer overwriting the blob between Pull
	// and Push. This bumps the stored ETag, so A's stashed ETag is now
	// stale — exactly the TOCTOU window the CAS guards.
	fs.objects["jitenv/blob"] = []byte("v2-by-other-host")
	fs.etags["jitenv/blob"] = "\"changed-by-other\""

	// A's push must be rejected at the precondition check.
	err := a.Push(context.Background(), []byte("v3-by-A"), syncadapter.Meta{Hash: "h3", SchemaVersion: 1})
	if !errors.Is(err, syncadapters.ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed on stale-ETag push, got %v", err)
	}
}

// TestS3PushFirstPushCASRejectsRace: two concurrent first pushes —
// after one lands, the other (which started with lastObserved=false
// from its own Pull) must fail with ErrPreconditionFailed via the
// IfNoneMatch: "*" guard (#278).
func TestS3PushFirstPushCASRejectsRace(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)

	// Adapter's Pull sees nothing (clean state).
	if _, _, err := a.Pull(context.Background()); err != syncadapters.ErrNoRemoteState {
		t.Fatalf("setup: expected ErrNoRemoteState, got %v", err)
	}

	// A third party "wins" the race and writes the blob first.
	fs.objects["jitenv/blob"] = []byte("racer-wrote-first")
	fs.etags["jitenv/blob"] = "\"racer\""

	err := a.Push(context.Background(), []byte("our-payload"), syncadapter.Meta{Hash: "h", SchemaVersion: 1})
	if !errors.Is(err, syncadapters.ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed on first-push race, got %v", err)
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

// TestS3ClientConcurrentInitNoRace exercises the lazy client() init from
// many goroutines so the race detector catches an unsynchronized lazy
// build (the syncadapter.Adapter contract requires concurrency safety).
func TestS3ClientConcurrentInitNoRace(t *testing.T) {
	fs := newFakeS3()
	a := newFakeAdapter(t, fs, nil)

	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			if _, err := a.client(context.Background()); err != nil {
				t.Errorf("client: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
}
