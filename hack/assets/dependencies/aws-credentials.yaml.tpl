---
apiVersion: v1
data:
  AccessKeyID: 
  SecretAccessKey: 
kind: Secret
metadata:
  name: aws-credentials
  namespace: kubernetes-kops-operator-system
type: Opaque
---
apiVersion: v1
data:
  creds:
kind: Secret
metadata:
  name: aws-credentials
  namespace: provider-crossplane-system
type: Opaque