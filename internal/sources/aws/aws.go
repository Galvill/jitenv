// Package aws implements the AWS Secrets Manager Source.
package aws

import (
	"context"
	"encoding/json"
	"fmt"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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
		{Key: "profile", Label: "Shared-config profile", Help: "from ~/.aws/credentials; optional"},
		{Key: "role_arn", Label: "Assume-role ARN", Sensitive: true, Help: "optional; assumed via STS"},
	}
}

// New constructs an AWS Secrets Manager source. Recognized cfg keys:
//
//	region   string  (optional; falls back to default chain)
//	profile  string  (optional; AWS_PROFILE)
//	role_arn string  (optional; assumed via STS)
func New(cfg map[string]any) (source.Source, error) {
	region, _ := cfg["region"].(string)
	profile, _ := cfg["profile"].(string)
	roleArn, _ := cfg["role_arn"].(string)
	return &awsSource{region: region, profile: profile, roleArn: roleArn}, nil
}

type awsSource struct {
	region  string
	profile string
	roleArn string
}

func (a *awsSource) Name() string { return TypeName }

func (a *awsSource) Schema() []source.ParamField { return schema() }

func (a *awsSource) loadAwsCfg(ctx context.Context) (awsv2.Config, error) {
	opts := []func(*config.LoadOptions) error{}
	if a.region != "" {
		opts = append(opts, config.WithRegion(a.region))
	}
	if a.profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(a.profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return awsv2.Config{}, err
	}
	if a.roleArn != "" {
		stsClient := sts.NewFromConfig(cfg)
		cfg.Credentials = stscreds.NewAssumeRoleProvider(stsClient, a.roleArn)
	}
	return cfg, nil
}

func (a *awsSource) Validate(ctx context.Context) error {
	cfg, err := a.loadAwsCfg(ctx)
	if err != nil {
		return err
	}
	stsClient := sts.NewFromConfig(cfg)
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
	cli := secretsmanager.NewFromConfig(cfg)
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
