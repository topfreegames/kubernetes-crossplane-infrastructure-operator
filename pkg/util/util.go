package util

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func awsConfigForCredential(ctx context.Context, region string, accessKey string, secretAccessKey string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     accessKey,
				SecretAccessKey: secretAccessKey,
			},
		}),
	)
}

// returns (secretName, awsConfig, err)
func AwsCredentialsFromKcp(ctx context.Context, c client.Client, kcp *kcontrolplanev1alpha1.KopsControlPlane) (string, *aws.Config, error) {
	subnet, err := kops.GetSubnetFromKopsControlPlane(kcp)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get subnet from kcp: %w", err)
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get region from subnet: %w", err)
	}

	secretName := kcp.Spec.IdentityRef.Name
	namespace := kcp.Spec.IdentityRef.Namespace

	awsCreds := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}
	if err := c.Get(ctx, key, awsCreds); err != nil {
		return "", nil, fmt.Errorf("failed to get secret: %w", err)
	}

	accessKeyBytes, ok := awsCreds.Data["AccessKeyID"]
	if !ok {
		return "", nil, fmt.Errorf("AWS secret %s in namespace %s does not contain AccessKeyID", secretName, namespace)
	}
	accessKey := string(accessKeyBytes)

	secretAccessKeyBytes, ok := awsCreds.Data["SecretAccessKey"]
	if !ok {
		return "", nil, fmt.Errorf("AWS secret %s in namespace %s does not contain SecretAccessKey", secretName, namespace)
	}
	secretAccessKey := string(secretAccessKeyBytes)

	cfg, err := awsConfigForCredential(ctx, *region, accessKey, secretAccessKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate AWS config: %w", err)
	}

	return secretName, &cfg, nil
}
