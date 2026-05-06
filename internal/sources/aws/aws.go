// Package aws implements the AWS Secrets Manager Source.
package aws

import (
	"context"
	"encoding/json"
	"fmt"

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
			Help: "Leave all credential fields empty to fall back to the AWS default chain (env vars / shared config / instance role)."},
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
		{Key: "profile", Label: "Shared-config profile",
			Help: "Optional: AWS_PROFILE name when falling back to the default chain. Ignored if Access key ID is set."},
	}
}

// New constructs an AWS Secrets Manager source.
//
// Credential resolution order:
//
//   - access_key_id non-empty → static credentials from the UI fields
//     (secret_access_key required; session_token optional).
//   - access_key_id empty     → AWS default credential chain
//     (env vars, shared config, instance/IRSA), optionally scoped to
//     `profile`.
//
// In either case, if `role_arn` is set, the source assumes that role via
// STS using the resolved base credentials. `endpoint_override`, when
// set, applies to both STS and Secrets Manager clients (LocalStack etc.).
func New(cfg map[string]any) (source.Source, error) {
	s := &awsSource{
		region:            asString(cfg["region"]),
		accessKeyID:       asString(cfg["access_key_id"]),
		secretAccessKey:   asString(cfg["secret_access_key"]),
		sessionToken:      asString(cfg["session_token"]),
		roleArn:           asString(cfg["role_arn"]),
		roleExternalID:    asString(cfg["role_external_id"]),
		roleSessionName:   asString(cfg["role_session_name"]),
		endpointOverride:  asString(cfg["endpoint_override"]),
		profile:           asString(cfg["profile"]),
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

type awsSource struct {
	region           string
	accessKeyID      string
	secretAccessKey  string
	sessionToken     string
	roleArn          string
	roleExternalID   string
	roleSessionName  string
	endpointOverride string
	profile          string
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

	switch {
	case a.accessKeyID != "":
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(a.accessKeyID, a.secretAccessKey, a.sessionToken),
		))
	case a.profile != "":
		opts = append(opts, config.WithSharedConfigProfile(a.profile))
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
