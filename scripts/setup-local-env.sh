#!/bin/bash
# Assume IAM role and update credentials in .env (preserves other settings)

set -e

ROLE_ARN="arn:aws:iam::877923746456:role/IAMS3AccessRole-unblocked-gh-actions-s3-cache"
PROFILE="deploybot"

echo "Assuming role: $ROLE_ARN (profile: $PROFILE)"

CREDS=$(aws sts assume-role \
    --profile "$PROFILE" \
    --role-arn "$ROLE_ARN" \
    --role-session-name "local-testing" \
    --output json)

ACCESS_KEY=$(echo "$CREDS" | jq -r '.Credentials.AccessKeyId')
SECRET_KEY=$(echo "$CREDS" | jq -r '.Credentials.SecretAccessKey')
SESSION_TOKEN=$(echo "$CREDS" | jq -r '.Credentials.SessionToken')
EXPIRATION=$(echo "$CREDS" | jq -r '.Credentials.Expiration')

# Update credentials in .env (preserves other settings)
sed -i '' "s|^export AWS_ACCESS_KEY_ID=.*|export AWS_ACCESS_KEY_ID=$ACCESS_KEY|" .env
sed -i '' "s|^export AWS_SECRET_ACCESS_KEY=.*|export AWS_SECRET_ACCESS_KEY=$SECRET_KEY|" .env
sed -i '' "s|^export AWS_SESSION_TOKEN=.*|export AWS_SESSION_TOKEN=\"$SESSION_TOKEN\"|" .env

echo "Credentials updated in .env (expires: $EXPIRATION)"

