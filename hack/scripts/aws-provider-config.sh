#!/bin/bash

set -e 

aws_access_key_id="${AWS_ACCESS_KEY_ID}"
aws_secret_access_key="${AWS_SECRET_ACCESS_KEY}"
aws_session_token="${AWS_SESSION_TOKEN}"
aws_full_credentials="[default]
aws_access_key_id=${aws_access_key_id}
aws_secret_access_key=${aws_secret_access_key}
aws_session_token=${aws_session_token}"

base64encode() {
  if [[ "$OSTYPE" == "darwin"* ]]; then
    base64
  else
    base64 -w0
  fi
}

# sed -e "s/aws_full_credentials/$(echo "${aws_full_credentials}" | base64encode)/g" \
sed -e "s/aws_full_credentials/$(echo "${aws_full_credentials}" | base64encode)/g" \
    -e "s/aws_access_key_id/$(echo "${aws_access_key_id}" | base64encode)/g" \
    -e "s/aws_secret_access_key/$(echo "${aws_secret_access_key}" | base64encode)/g" \
    ./hack/assets/dependencies/aws-credentials.yaml.tpl \
    | kubectl apply -f -