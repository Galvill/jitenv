#!/usr/bin/env bash
# Seed AWS Secrets Manager with a fixture secret used by the
# localstack scenarios. The awscli is preinstalled in the LocalStack
# image, and `awslocal` wraps it with the right endpoint.
#
# We create one secret with a JSON body so scenarios can exercise both
# the "fetch one key" path (ref.Key="FOO") and the "expand all keys"
# path (empty Name + empty Key) without re-seeding.
set -euo pipefail

SECRET_ARN="arn:aws:secretsmanager:us-east-1:000000000000:secret:jitenv/demo"

echo "[seed] creating jitenv/demo secret"
awslocal secretsmanager create-secret \
    --name jitenv/demo \
    --secret-string '{"FOO":"value-from-aws-foo","BAR":"value-from-aws-bar"}' \
    >/dev/null 2>&1 || awslocal secretsmanager put-secret-value \
        --secret-id jitenv/demo \
        --secret-string '{"FOO":"value-from-aws-foo","BAR":"value-from-aws-bar"}' \
        >/dev/null

# Re-emit the canonical ARN so the harness can grep for it in logs
# when something goes wrong.
echo "[seed] secret_arn=$SECRET_ARN"
