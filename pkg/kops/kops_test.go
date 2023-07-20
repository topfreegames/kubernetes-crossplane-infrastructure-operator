package kops

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	. "github.com/onsi/ginkgo/v2"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
)

func TestGetRegionFromKopsControlPlane(t *testing.T) {

	testCases := []struct {
		description          string
		input                *kcontrolplanev1alpha1.KopsControlPlane
		expectedRegion       string
		expectedErrorMessage string
	}{
		{
			description: "should return us-east-1",
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					KopsClusterSpec: kopsapi.ClusterSpec{
						Subnets: []kopsapi.ClusterSubnetSpec{
							{
								Name: "us-east-1a",
								Zone: "us-east-1a",
							},
						},
					},
				},
			},
			expectedRegion: "us-east-1",
		},
		{
			description: "should fail to get region without subnet",
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					KopsClusterSpec: kopsapi.ClusterSpec{},
				},
			},
			expectedErrorMessage: "subnet not found",
		},
		{
			description: "should fail to get region with invalid subnet",
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					KopsClusterSpec: kopsapi.ClusterSpec{
						Subnets: []kopsapi.ClusterSubnetSpec{
							{
								Name: "",
								Zone: "",
							},
						},
					},
				},
			},
			expectedErrorMessage: "couldn't get region from KopsControlPlane",
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			region, err := GetRegionFromKopsControlPlane(context.TODO(), tc.input)
			if len(tc.expectedErrorMessage) != 0 {
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorMessage))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(*region).To(Equal(tc.expectedRegion))
			}
		})
	}
}

func TestRetrieveAWSCredentialsFromKCP(t *testing.T) {

	testCases := []struct {
		description          string
		input                *kcontrolplanev1alpha1.KopsControlPlane
		k8sObjects           []client.Object
		expectedErrorMessage string
	}{
		{
			description: "should fail to get aws credentials with invalid identity ref",
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					IdentityRef: &corev1.ObjectReference{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
				},
			},
			expectedErrorMessage: "failed to get secret",
		},
		{
			description: "should fail to get aws credentials with secret missing AccessKeyID",
			k8sObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
					Data: map[string][]byte{
						"SecretAccessKey": []byte("a"),
					},
				},
			},
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					IdentityRef: &corev1.ObjectReference{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
				},
			},
			expectedErrorMessage: "does not contain AccessKeyID",
		},
		{
			description: "should fail to get aws credentials with secret missing AccessKeyID",
			k8sObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
					Data: map[string][]byte{
						"AccessKeyID": []byte("a"),
					},
				},
			},
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					IdentityRef: &corev1.ObjectReference{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
				},
			},
			expectedErrorMessage: "does not contain SecretAccessKey",
		},
		{
			description: "should succeed to get aws credentials config",
			k8sObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
					Data: map[string][]byte{
						"AccessKeyID":     []byte("a"),
						"SecretAccessKey": []byte("a"),
					},
				},
			},
			input: &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcp-test",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					IdentityRef: &corev1.ObjectReference{
						Name:      "aws-credentials",
						Namespace: corev1.NamespaceDefault,
					},
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			_, _, err := RetrieveAWSCredentialsFromKCP(context.TODO(), fakeClient, aws.String("us-east-1"), tc.input)
			if len(tc.expectedErrorMessage) != 0 {
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorMessage))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
