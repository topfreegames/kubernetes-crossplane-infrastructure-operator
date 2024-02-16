---
apiVersion: v1
data:
  AccessKeyID: aws_access_key_id
  SecretAccessKey: aws_secret_access_key
kind: Secret
metadata:
  name: aws-credentials
  namespace: kubernetes-kops-operator-system
type: Opaque
---
apiVersion: v1
data:
  creds:
    aws_full_credentials
kind: Secret
metadata:
  name: aws-credentials
  namespace: kubernetes-crossplane-infrastructure-operator-system
type: Opaque