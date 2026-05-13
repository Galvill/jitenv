// Package aws implements the AWS Secrets Manager Source.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

const TypeName = "aws"

func init() {
	sources.Register(TypeName, New)
	sources.RegisterSchema(TypeName, schema())
}

func schema() []source.ParamField {
	return []source.ParamField{
		{Key: "region", Label: "AWS region", Required: true, Help: "e.g. us-east-1"},
		{Key: "access_key_id", Label: "Access key ID",
			Help: "Leave credential fields empty to use the AWS default chain."},
		{Key: "secret_access_key", Label: "Secret access key", Sensitive: true,
			Help: "Encrypted at rest under the master key."},
		{Key: "session_token", Label: "Session token", Sensitive: true,
			Help: "Optional STS session token (encrypted at rest)."},
		{Key: "role_arn", Label: "Assume-role ARN",
			Help: "Optional: assume this role via STS after the credentials above load."},
		{Key: "role_external_id", Label: "Assume-role external ID", Sensitive: true,
			Help: "Optional: external-ID for cross-account assume-role (encrypted at rest)."},
		{Key: "role_session_name", Label: "Assume-role session name",
			Help: "Optional; default: jitenv"},
		{Key: "endpoint_override", Label: "Endpoint URL override",
			Help: "Optional: override STS / Secrets Manager endpoints (e.g. http://localhost:4566 for LocalStack)."},
	}
}

// errProfileRemoved is returned by New when a stored config still
// carries a non-empty `profile` value. Falling silently back to the
// default credential chain risks loading the wrong AWS account, so we
// fail hard and tell the user how to migrate.
const errProfileRemoved = `the "profile" field has been removed from the AWS source.
Clear it from your config and set AWS_PROFILE in your environment instead.`

// New constructs an AWS Secrets Manager source.
//
// Credential resolution order:
//
//   - access_key_id non-empty → static credentials from the UI fields
//     (secret_access_key required; session_token optional).
//   - access_key_id empty     → AWS default credential chain
//     (env vars, shared config, instance/IRSA).
//
// In either case, if `role_arn` is set, the source assumes that role via
// STS using the resolved base credentials. `endpoint_override`, when
// set, applies to both STS and Secrets Manager clients (LocalStack etc.).
//
// `arns`, when present, is the user-curated list of secret ARNs this
// source is allowed to read. The TUI surfaces them as bags in the
// var-tree picker; the agent only ever Fetches IDs the user explicitly
// added.
//
// The legacy `profile` field is no longer accepted; a non-empty value
// in stored config trips a hard error so credential resolution never
// silently drifts to the wrong account. Migrate by clearing the field
// and exporting AWS_PROFILE in the environment.
func New(cfg map[string]any) (source.Source, error) {
	if p := asString(cfg["profile"]); p != "" {
		return nil, errors.New(errProfileRemoved)
	}
	s := &awsSource{
		region:           asString(cfg["region"]),
		accessKeyID:      asString(cfg["access_key_id"]),
		secretAccessKey:  asString(cfg["secret_access_key"]),
		sessionToken:     asString(cfg["session_token"]),
		roleArn:          asString(cfg["role_arn"]),
		roleExternalID:   asString(cfg["role_external_id"]),
		roleSessionName:  asString(cfg["role_session_name"]),
		endpointOverride: asString(cfg["endpoint_override"]),
		arns:             asStringList(cfg["arns"]),
	}
	if s.accessKeyID != "" && s.secretAccessKey == "" {
		return nil, fmt.Errorf("aws: access_key_id is set but secret_access_key is empty")
	}
	return s, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// asStringList accepts either a TOML []any of strings (default after
// load) or a Go []string (when the TUI sets it before save).
func asStringList(v any) []string {
	switch x := v.(type) {
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

type awsSource struct {
	region           string
	accessKeyID      string
	secretAccessKey  string
	sessionToken     string
	roleArn          string
	roleExternalID   string
	roleSessionName  string
	endpointOverride string
	arns             []string
}

func (a *awsSource) Name() string { return TypeName }

func (a *awsSource) Schema() []source.ParamField { return schema() }

// loadAwsCfg returns an aws.Config wired to the credential source the
// user picked (static or ambient), with optional STS assume-role and
// endpoint override applied.
func (a *awsSource) loadAwsCfg(ctx context.Context) (awsv2.Config, error) {
	opts := []func(*config.LoadOptions) error{}
	if a.region != "" {
		opts = append(opts, config.WithRegion(a.region))
	}

	if a.accessKeyID != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(a.accessKeyID, a.secretAccessKey, a.sessionToken),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return awsv2.Config{}, err
	}

	if a.roleArn != "" {
		stsClient := sts.NewFromConfig(cfg, a.stsOptions()...)
		cfg.Credentials = stscreds.NewAssumeRoleProvider(stsClient, a.roleArn, func(o *stscreds.AssumeRoleOptions) {
			if a.roleExternalID != "" {
				o.ExternalID = awsv2.String(a.roleExternalID)
			}
			if a.roleSessionName != "" {
				o.RoleSessionName = a.roleSessionName
			} else {
				o.RoleSessionName = "jitenv"
			}
		})
	}

	return cfg, nil
}

// stsOptions and smOptions apply the endpoint override (if any) to the
// respective service clients. We keep the override on the *client*
// rather than the global aws.Config so users only redirect the calls
// jitenv actually makes.
func (a *awsSource) stsOptions() []func(*sts.Options) {
	if a.endpointOverride == "" {
		return nil
	}
	return []func(*sts.Options){
		func(o *sts.Options) { o.BaseEndpoint = awsv2.String(a.endpointOverride) },
	}
}

func (a *awsSource) smOptions() []func(*secretsmanager.Options) {
	if a.endpointOverride == "" {
		return nil
	}
	return []func(*secretsmanager.Options){
		func(o *secretsmanager.Options) { o.BaseEndpoint = awsv2.String(a.endpointOverride) },
	}
}

// Bags returns one Bag per configured ARN. Each bag is populated by
// running GetSecretValue on the ARN and parsing the JSON top-level
// keys. Scalar secrets land with empty Keys.
//
// Errors fetching one ARN do NOT abort the others: a failed bag is
// returned with empty Keys plus the underlying error preserved on
// the joined error so the TUI can flag it without losing the rest.
func (a *awsSource) Bags(ctx context.Context) ([]source.Bag, error) {
	if len(a.arns) == 0 {
		return nil, nil
	}
	cfg, err := a.loadAwsCfg(ctx)
	if err != nil {
		return nil, err
	}
	cli := secretsmanager.NewFromConfig(cfg, a.smOptions()...)
	out := make([]source.Bag, 0, len(a.arns))
	var perARN []error
	for _, arn := range a.arns {
		bag := source.Bag{RefID: arn, DisplayName: arnDisplayName(arn)}
		v, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: awsv2.String(arn),
		})
		if err != nil {
			perARN = append(perARN, fmt.Errorf("%s: %w", bag.DisplayName, err))
			out = append(out, bag)
			continue
		}
		if v.SecretString != nil {
			var obj map[string]any
			if jsonErr := json.Unmarshal([]byte(*v.SecretString), &obj); jsonErr == nil {
				keys := make([]string, 0, len(obj))
				for k := range obj {
					keys = append(keys, k)
				}
				bag.Keys = keys
			}
		}
		out = append(out, bag)
	}
	if len(perARN) > 0 {
		return out, errors.Join(perARN...)
	}
	return out, nil
}

// arnDisplayName extracts the user-facing secret name out of a
// Secrets Manager ARN, dropping the random `-XXXXXX` suffix AWS adds.
//
//	arn:aws:secretsmanager:us-east-1:1234:secret:prod/db-AbCdEf
//	                                          └────────┬────────┘
//	                                                   └──→ "prod/db"
//
// Falls back to the ARN itself when it doesn't fit the expected shape.
func arnDisplayName(arn string) string {
	const marker = ":secret:"
	i := strings.Index(arn, marker)
	if i < 0 {
		return arn
	}
	name := arn[i+len(marker):]
	// AWS appends `-` plus 6 alphanumerics. Strip if it matches.
	if len(name) > 7 && name[len(name)-7] == '-' {
		suffix := name[len(name)-6:]
		alnum := true
		for _, c := range suffix {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				alnum = false
				break
			}
		}
		if alnum {
			return name[:len(name)-7]
		}
	}
	return name
}

func (a *awsSource) Validate(ctx context.Context) error {
	cfg, err := a.loadAwsCfg(ctx)
	if err != nil {
		return err
	}
	stsClient := sts.NewFromConfig(cfg, a.stsOptions()...)
	_, err = stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return err
}

// Fetch returns secret material from AWS Secrets Manager.
//
//	ref.ID  → SecretId (name or ARN)
//	ref.Key → optional JSON sub-key. When set, the secret string MUST
//	          parse as a JSON object and the value at ref.Key is returned.
//	          When unset, the entire SecretString is returned under the key
//	          "value" (raw scalar) or, if the JSON parses, all top-level keys.
func (a *awsSource) Fetch(ctx context.Context, ref source.SecretRef) (map[string]string, error) {
	if ref.ID == "" {
		return nil, fmt.Errorf("aws: ref.ID (SecretId) is required")
	}
	cfg, err := a.loadAwsCfg(ctx)
	if err != nil {
		return nil, err
	}
	cli := secretsmanager.NewFromConfig(cfg, a.smOptions()...)
	out, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: awsv2.String(ref.ID),
	})
	if err != nil {
		return nil, fmt.Errorf("aws GetSecretValue %q: %w", ref.ID, err)
	}
	if out.SecretString == nil {
		return nil, fmt.Errorf("aws: secret %q has no SecretString (binary not supported)", ref.ID)
	}
	body := *out.SecretString

	if ref.Key != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(body), &obj); err != nil {
			return nil, fmt.Errorf("aws: secret %q is not JSON; cannot pick key %q", ref.ID, ref.Key)
		}
		v, ok := obj[ref.Key]
		if !ok {
			return nil, fmt.Errorf("aws: secret %q has no key %q", ref.ID, ref.Key)
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("aws: secret %q key %q is not a string", ref.ID, ref.Key)
		}
		return map[string]string{ref.Key: s}, nil
	}

	// No specific key requested. If the secret parses as a JSON object, return all keys.
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err == nil {
		out := map[string]string{}
		for k, v := range obj {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	// Otherwise treat as a raw scalar.
	return map[string]string{"value": body}, nil
}
