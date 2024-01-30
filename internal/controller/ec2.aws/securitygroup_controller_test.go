/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sgcontroller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/hashicorp/go-multierror"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	clustermeshv1alpha1 "github.com/topfreegames/provider-crossplane/api/clustermesh.infrastructure/v1alpha1"
	securitygroupv1alpha2 "github.com/topfreegames/provider-crossplane/api/ec2.aws/v1alpha2"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/provider-crossplane/pkg/aws/ec2/fake"
	"github.com/topfreegames/provider-crossplane/pkg/spot"
	fakeocean "github.com/topfreegames/provider-crossplane/pkg/spot/fake"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	sg = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha2.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: []*corev1.ObjectReference{
				{
					APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsMachinePool",
					Name:       "test-kops-machine-pool",
					Namespace:  metav1.NamespaceDefault,
				},
			},
		},
	}

	sgEmpty = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules:      []securitygroupv1alpha2.IngressRule{},
			InfrastructureRef: []*corev1.ObjectReference{},
		},
	}

	sgKCP = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha2.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: []*corev1.ObjectReference{
				{
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsControlPlane",
					Name:       "test-cluster",
					Namespace:  metav1.NamespaceDefault,
				},
			},
		},
	}

	csg = &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Status: crossec2v1beta1.SecurityGroupStatus{
			ResourceStatus: crossplanev1.ResourceStatus{
				ConditionedStatus: crossplanev1.ConditionedStatus{
					Conditions: []crossplanev1.Condition{
						{
							Type:   "Ready",
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			AtProvider: crossec2v1beta1.SecurityGroupObservation{
				SecurityGroupID: "sg-1",
			},
		},
	}

	kmp = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
		},
	}

	spotKMP = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
			SpotInstOptions: map[string]string{
				"spot": "true",
			},
		},
	}

	karpenterKMP = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				Manager: "Karpenter",
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
		},
	}

	cluster = &clusterv1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
		Spec: clusterv1beta1.ClusterSpec{
			ControlPlaneRef: &corev1.ObjectReference{
				APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				Kind:       "KopsControlPlane",
				Name:       "test-kops-control-plane",
				Namespace:  metav1.NamespaceDefault,
			},
		},
	}

	kcp = &kcontrolplanev1alpha1.KopsControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
		Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
			KopsClusterSpec: kopsapi.ClusterSpec{
				Networking: kopsapi.NetworkingSpec{
					Subnets: []kopsapi.ClusterSubnetSpec{
						{
							Name: "test-subnet",
							CIDR: "0.0.0.0/26",
							Zone: "us-east-1d",
						},
					},
					NetworkCIDR: "0.0.0.0/24",
				},
			},
			IdentityRef: kcontrolplanev1alpha1.IdentityRefSpec{
				Name:      "default",
				Namespace: "kubernetes-kops-operator-system",
			},
		},
	}

	kcpWithIdentityRef = &kcontrolplanev1alpha1.KopsControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
		Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
			KopsClusterSpec: kopsapi.ClusterSpec{
				Networking: kopsapi.NetworkingSpec{
					Subnets: []kopsapi.ClusterSubnetSpec{
						{
							Name: "test-subnet",
							CIDR: "0.0.0.0/26",
							Zone: "us-east-1d",
						},
					},
				},
			},
			IdentityRef: kcontrolplanev1alpha1.IdentityRefSpec{
				Name:      "test-secret",
				Namespace: "kubernetes-kops-operator-system",
			},
		},
	}

	secretForIdentityRef = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kubernetes-kops-operator-system",
			Name:      "test-secret",
		},
		Data: map[string][]byte{
			"AccessKeyID":     []byte("AK"),
			"SecretAccessKey": []byte("SAK"),
		},
	}

	defaultSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kubernetes-kops-operator-system",
			Name:      "default",
		},
		Data: map[string][]byte{
			"AccessKeyID":     []byte("AK"),
			"SecretAccessKey": []byte("SAK"),
		},
	}
)

func TestSecurityGroupReconciler(t *testing.T) {

	testCases := []struct {
		description               string
		k8sObjects                []client.Object
		errorExpected             error
		expectedDeletion          bool
		expectedProviderConfigRef *string
	}{
		{
			description: "should fail without InfrastructureRef defined",
			k8sObjects: []client.Object{
				kmp, cluster, kcp,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
					},
				},
			},
			errorExpected: errors.New("infrastructureRef not supported"),
		},
		{
			description: "should create a SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP,
			},
		},
		{
			description: "should update a SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, csg,
			},
		},
		{
			description: "should create a Crossplane SecurityGroup with KopsControlPlane infrastructureRef and return that SecurityGroups isn't available",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sg,
			},
			errorExpected: ErrSecurityGroupNotAvailable,
		},
		{
			description: "should update an available Crossplane SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, csg,
			},
		},
		{
			description: "should create a Crossplane SecurityGroup with KopsMachinePool infrastructureRef and return that SecurityGroups isn't available",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sg,
			},
			errorExpected: ErrSecurityGroupNotAvailable,
		},
		{
			description: "should update an available Crossplane SecurityGroup with KopsMachinePool infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sg, csg,
			},
		},
		{
			description: "should fail with InfrastructureRef Kind different from KopsMachinePool and KopsControlPlane",
			k8sObjects: []client.Object{
				kmp, cluster, kcp,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "MachinePool",
								Name:       "test-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
			},
			errorExpected: errors.New("infrastructureRef not supported"),
		},
		{
			description: "should remove SecurityGroup with DeletionTimestamp",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sgKCP,
			},
			expectedDeletion: true,
		},
		{
			description: "should create a SecurityGroup with the same providerConfigName",
			k8sObjects: []client.Object{
				kmp, cluster, kcpWithIdentityRef, secretForIdentityRef, sgKCP, csg,
			},
			expectedProviderConfigRef: &kcpWithIdentityRef.Spec.IdentityRef.Name,
		},
		{
			description: "should fail when not finding KopsMachinePool",
			k8sObjects: []client.Object{
				cluster, kcp, sg, defaultSecret,
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "infrastructure.cluster.x-k8s.io", Resource: "kopsmachinepools"}, kmp.Name),
		},
		{
			description: "should fail when not finding KopsControlPlane",
			k8sObjects: []client.Object{
				kmp, cluster, sgKCP, defaultSecret,
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, kcp.Name),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).WithStatusSubresource(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						LaunchTemplateId: params.LaunchTemplateId,
						VersionNumber:    aws.Int64(2),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}
			fakeASGClient.MockUpdateAutoScalingGroup = func(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
				Expect(*params.LaunchTemplate.Version).To(BeEquivalentTo("2"))
				return &awsautoscaling.UpdateAutoScalingGroupOutput{}, nil
			}
			reconciler := &SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(5),
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Name: "test-security-group",
				},
			})

			if tc.errorExpected == nil {
				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Name: sg.ObjectMeta.Name,
				}
				err = fakeClient.Get(ctx, key, crosssg)
				if !tc.expectedDeletion {
					g.Expect(err).To(BeNil())
					g.Expect(crosssg).NotTo(BeNil())
				} else {
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}

				if tc.expectedProviderConfigRef != nil {
					g.Expect(crosssg.Spec.ProviderConfigReference.Name).To(Equal(*tc.expectedProviderConfigRef))
				}
			} else {
				g.Expect(err).To(MatchError(tc.errorExpected))
			}
		})
	}
}

func TestRetrieveInfraRefInfo(t *testing.T) {

	testCases := []struct {
		description          string
		k8sObjects           []client.Object
		errorExpected        error
		targetSG             *securitygroupv1alpha2.SecurityGroup
		mockUpdateLaunchSpec func(context.Context, *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error)
	}{
		{
			description: "should return error when infrastructureRef empty",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG:      sgEmpty,
			errorExpected: errors.New("no infrastructureRef found"),
		},
		{
			description: "should return error when all refs fail to get KMP",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG:      sg,
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "infrastructure.cluster.x-k8s.io", Resource: "kopsmachinepools"}, kmp.Name),
		},
		{
			description: "should return error when all refs fail to get KCP of KMP",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG:      sg,
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should return nil",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: sg,
		},
		{
			description: "should return error when all refs fail to get KCP",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG:      sgKCP,
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should return error when all refs are from unknown type",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-fake-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: errors.New("infrastructureRef not supported"),
		},
		{
			description: "should not fail when one of the refs kmp does not exists",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should not fail when one of the refs kmp does not exists but kcp exists",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when all refs are incomplete kmp",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should not fail when one of the refs kcp does not exists but kmp exists",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should not fail when one of the refs kcp does not exists",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when all refs are incomplete",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should fail when all refs are incomplete kcp",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-2",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, "test-cluster-2"),
		},
		{
			description: "should not fail when one of the refs is type unknown but kmp exists",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when one of the refs is type unknown but kmp does not have kcp",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should not fail when one of the refs is type unknown and kcp exist",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when one of the refs is type unknown but kcp does not exist",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}

			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      tc.targetSG,
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			err = reconciliation.retrieveInfraRefInfo(ctx)
			if tc.errorExpected == nil {
				if !errors.Is(err, ErrSecurityGroupNotAvailable) {
					g.Expect(err).To(BeNil())
				}

			} else {
				g.Expect(tc.errorExpected).To(MatchError(err))
			}
		})
	}
}

func TestAttachKopsMachinePool(t *testing.T) {

	testCases := []struct {
		description          string
		k8sObjects           []client.Object
		kmp                  *kinfrastructurev1alpha1.KopsMachinePool
		errorExpected        error
		mockUpdateLaunchSpec func(context.Context, *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error)
	}{
		{
			description: "should reconcile without error with spotKMP",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				sg,
				csg,
				defaultSecret,
			},
			kmp: spotKMP,
		},
		{
			description: "should reconcile without error with karpenter KMP",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				sg,
				csg,
				defaultSecret,
			},
			kmp: karpenterKMP,
		},
		{
			description: "should fail with wrong spotKMP clusterName",
			k8sObjects: []client.Object{
				cluster,
				sg,
				csg,
				defaultSecret,
				&kcontrolplanev1alpha1.KopsControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-cluster-2",
					},
					Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
						KopsClusterSpec: kopsapi.ClusterSpec{
							Networking: kopsapi.NetworkingSpec{
								Subnets: []kopsapi.ClusterSubnetSpec{
									{
										Name: "test-subnet",
										CIDR: "0.0.0.0/26",
										Zone: "us-east-1d",
									},
								},
							},
						},
					},
				},
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-machine-pool",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster-2",
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster-2",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
						SpotInstOptions: map[string]string{
							"spot": "true",
						},
					},
				},
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster-2",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster-2",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
							"kops.k8s.io/instance-group-role": "Node",
						},
					},
					SpotInstOptions: map[string]string{
						"spot": "true",
					},
				},
			},
			errorExpected: errors.New("error retrieving vng's from cluster: test-cluster-2"),
		},
		{
			description: "should fail if cant match vng with kmp name",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool-2",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
				csg,
				&kcontrolplanev1alpha1.KopsControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-control-plane",
					},
					Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
						KopsClusterSpec: kopsapi.ClusterSpec{
							Networking: kopsapi.NetworkingSpec{
								Subnets: []kopsapi.ClusterSubnetSpec{
									{
										Name: "test-subnet",
										CIDR: "0.0.0.0/26",
										Zone: "us-east-1d",
									},
								},
							},
						},
					},
				},
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-machine-pool-2",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster",
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
						SpotInstOptions: map[string]string{
							"spot": "true",
						},
					},
				},
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool-2",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
							"kops.k8s.io/instance-group-role": "Node",
						},
					},
					SpotInstOptions: map[string]string{
						"spot": "true",
					},
				},
			},
			errorExpected: multierror.Append(errors.New("error no vng found with instance group name: test-kops-machine-pool-2")),
		},
		{
			description: "should fail when failing to attach",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, csg, sg, defaultSecret,
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
						},
					},
				},
			},
			errorExpected: multierror.Append(errors.New("failed to retrieve role from KopsMachinePool test-kops-machine-pool")),
		},
		{
			description: "should fail when failing to attach with karpenterKMP",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, csg, sg, defaultSecret,
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						Manager: "Karpenter",
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
						},
					},
				},
			},
			errorExpected: multierror.Append(errors.New("failed to retrieve role from KopsMachinePool test-kops-machine-pool")),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{}, nil
			}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
				return &awsec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []ec2types.LaunchTemplate{
						{
							LaunchTemplateId:   aws.String("lt-xxxx"),
							LaunchTemplateName: aws.String("test-launch-template"),
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("tag:KubernetesCluster"),
									Value: aws.String("test-cluster"),
								},
								{
									Key:   aws.String("tag:Name"),
									Value: aws.String("test-launch-template"),
								},
							},
						},
					},
				}, nil
			}

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}

			fakeOceanClient.MockListClusters = func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test-cluster"),
						},
					},
				}, nil
			}
			fakeOceanClient.MockListLaunchSpecs = func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
				return &oceanaws.ListLaunchSpecsOutput{
					LaunchSpecs: []*oceanaws.LaunchSpec{
						{
							ID:      aws.String("1"),
							Name:    aws.String("vng-test"),
							OceanID: aws.String("o-1"),
							Labels: []*oceanaws.Label{
								{
									Key:   aws.String("kops.k8s.io/instance-group-name"),
									Value: aws.String("test-kops-machine-pool"),
								},
							},
						},
					},
				}, nil
			}
			if tc.mockUpdateLaunchSpec != nil {
				fakeOceanClient.MockUpdateLaunchSpec = tc.mockUpdateLaunchSpec
			} else {
				fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
					return &oceanaws.UpdateLaunchSpecOutput{}, nil
				}
			}

			recorder := record.NewFakeRecorder(5)

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
				Recorder: recorder,
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      sg,
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			err = reconciliation.attachKopsMachinePool(ctx, csg, kcp, *tc.kmp)
			if tc.errorExpected == nil {
				if !errors.Is(err, ErrSecurityGroupNotAvailable) {
					g.Expect(err).To(BeNil())
				}

			} else {
				g.Expect(tc.errorExpected).To(MatchError(err))
			}
		})
	}
}

func TestAttachSGToVNG(t *testing.T) {
	testCases := []struct {
		description            string
		k8sObjects             []client.Object
		kmp                    *kinfrastructurev1alpha1.KopsMachinePool
		csg                    *crossec2v1beta1.SecurityGroup
		launchSpecs            []*oceanaws.LaunchSpec
		instances              []ec2types.Instance
		expectedSecurityGroups []string
		errorExpected          error
	}{
		{
			description: "should attach SG to VNG",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
				},
				{
					ID:   aws.String("2"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String("test-kops-machine-pool-2"),
						},
					},
				},
			},
			expectedSecurityGroups: []string{"sg-1"},
		},
		{
			description: "should do nothing when SG is already attached",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-1",
					},
				},
			},
			expectedSecurityGroups: []string{"sg-1"},
		},
		{
			description:   "should fail if cant match vng with kmp name",
			kmp:           spotKMP,
			csg:           csg,
			launchSpecs:   []*oceanaws.LaunchSpec{},
			errorExpected: fmt.Errorf("error no vng found with instance group name: %s", spotKMP.Name),
		},
		{
			description: "should attach sg in the current vng instances",
			kmp:         spotKMP,
			csg:         csg,
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("1"),
				},
			},
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
				},
				{
					ID:   aws.String("2"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String("test-kops-machine-pool-2"),
						},
					},
				},
			},
			expectedSecurityGroups: []string{"sg-1"},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{
						{
							Instances: []ec2types.Instance{
								{
									InstanceId: aws.String("i-xxx"),
									State: &ec2types.InstanceState{
										Name: ec2types.InstanceStateNameRunning,
									},
								},
								{
									InstanceId: aws.String("i-yyy"),
									State: &ec2types.InstanceState{
										Name: ec2types.InstanceStateNameTerminated,
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}
			fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
				g.Expect(updateLaunchSpecInput.LaunchSpec.SecurityGroupIDs).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &oceanaws.UpdateLaunchSpecOutput{}, nil
			}
			reconciler := SecurityGroupReconciler{
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				ec2Client:               fakeEC2Client,
				log:                     ctrl.LoggerFrom(ctx),
			}

			err = reconciliation.attachSGToVNG(ctx, fakeOceanClient, tc.launchSpecs, tc.kmp.Name, csg)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(MatchError(tc.errorExpected))
			}
		})
	}
}

func TestAttachSGToASG(t *testing.T) {
	testCases := []struct {
		description            string
		k8sObjects             []client.Object
		asgs                   []autoscalingtypes.AutoScalingGroup
		instances              []ec2types.Instance
		expectedSecurityGroups []string
		errorExpected          error
	}{
		{
			description: "should attach SecurityGroup to the ASG",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},

			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should attach SecurityGroup to the ASG instance",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
					Instances: []autoscalingtypes.Instance{
						{
							InstanceId: aws.String("i-xxx"),
						},
					},
				},
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should fail when not finding asg",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			errorExpected: errors.New("failed to retrieve AutoScalingGroup"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{
						{
							Instances: tc.instances,
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				g.Expect(params.LaunchTemplateData.NetworkInterfaces[0].Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						LaunchTemplateId: params.LaunchTemplateId,
						VersionNumber:    aws.Int64(2),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: tc.asgs,
				}, nil
			}
			fakeASGClient.MockUpdateAutoScalingGroup = func(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
				Expect(*params.LaunchTemplate.Version).To(BeEquivalentTo("2"))
				return &awsautoscaling.UpdateAutoScalingGroupOutput{}, nil
			}
			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				asgClient:               fakeASGClient,
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.attachSGToASG(ctx, "test-asg", "sg-yyyy")
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring((tc.errorExpected.Error())))
			}
		})
	}
}

func TestAttachSGToLaunchTemplate(t *testing.T) {
	testCases := []struct {
		description                       string
		k8sObjects                        []client.Object
		instances                         []ec2types.Instance
		expectedSecurityGroups            []string
		errorExpected                     error
		errorGetLaunchTemplate            bool
		errorGetLastLaunchTemplateVersion bool
		errorCreateLaunchTemplateVersion  bool
		errorGetReservations              bool
		errorAttachIGToInstances          bool
	}{
		{
			description: "should attach SecurityGroup to the launch template",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should attach SecurityGroup to the launch template instance",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should fail when not finding launch template",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:          errors.New("some error when retrieving launch template"),
			errorGetLaunchTemplate: true,
		},
		{
			description: "should fail when not finding launch template version",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			expectedSecurityGroups:            []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:                     errors.New("some error when retrieving launch template version"),
			errorGetLastLaunchTemplateVersion: true,
		},
		{
			description: "should fail when not create launch template version",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			expectedSecurityGroups:           []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:                    errors.New("some error when creating launch template version"),
			errorCreateLaunchTemplateVersion: true,
		},
		{
			description: "should fail when not able to get reservations",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:          errors.New("some error when get reservations"),
			errorGetReservations:   true,
		},
		{
			description: "should fail when not able to attach ig to instances",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg,
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedSecurityGroups:   []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:            errors.New("some error when attach ig to instances"),
			errorAttachIGToInstances: true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			if tc.errorGetLastLaunchTemplateVersion {
				fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
					return nil, errors.New("some error when retrieving launch template version")
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
					return &awsec2.DescribeLaunchTemplateVersionsOutput{
						LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
							{
								LaunchTemplateId: params.LaunchTemplateId,
								LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
									NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
										{
											Groups: []string{
												"sg-xxxx",
											},
										},
									},
								},
							},
						},
					}, nil
				}
			}

			if tc.errorAttachIGToInstances {
				fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
					g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return nil, errors.New("some error when attach ig to instances")
				}
			} else {
				fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
					g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return &awsec2.ModifyInstanceAttributeOutput{}, nil
				}
			}

			if tc.errorGetReservations {
				fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
					return nil, errors.New("some error when get reservations")
				}
			} else {
				fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
					return &awsec2.DescribeInstancesOutput{
						Reservations: []ec2types.Reservation{
							{
								Instances: tc.instances,
							},
						},
					}, nil
				}
			}

			if tc.errorCreateLaunchTemplateVersion {
				fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
					g.Expect(params.LaunchTemplateData.NetworkInterfaces[0].Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return nil, errors.New("some error when creating launch template version")
				}
			} else {
				fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
					g.Expect(params.LaunchTemplateData.NetworkInterfaces[0].Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return &awsec2.CreateLaunchTemplateVersionOutput{
						LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
							LaunchTemplateId: params.LaunchTemplateId,
							VersionNumber:    aws.Int64(2),
						},
					}, nil
				}
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			if tc.errorGetLaunchTemplate {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
					return nil, errors.New("some error when retrieving launch template")
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
					return &awsec2.DescribeLaunchTemplatesOutput{
						LaunchTemplates: []ec2types.LaunchTemplate{
							{
								LaunchTemplateId:   aws.String("lt-xxxx"),
								LaunchTemplateName: aws.String("test-launch-template"),
								Tags: []ec2types.Tag{
									{
										Key:   aws.String("tag:KubernetesCluster"),
										Value: aws.String("test-cluster"),
									},
									{
										Key:   aws.String("tag:Name"),
										Value: aws.String("test-launch-template"),
									},
								},
							},
						},
					}, nil
				}
			}

			reconciler := SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(3),
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.attachSGToLaunchTemplate(ctx, "test-cluster", "test-launch-template", "sg-yyyy")
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring((tc.errorExpected.Error())))
			}
		})
	}
}

func TestReconcileDelete(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description   string
		k8sObjects    []client.Object
		sg            *securitygroupv1alpha2.SecurityGroup
		expectedError error
	}{
		{
			description: "should remove the crossplane security group referencing kmp",
			k8sObjects: []client.Object{
				sg, csg, kcp, kmp, cluster, defaultSecret,
			},
			sg: sg,
		},
		{
			description: "should remove the crossplane security group referencing kmp using kops",
			k8sObjects: []client.Object{
				sg, csg, kcp, spotKMP, cluster, defaultSecret,
			},
			sg: sg,
		},
		{
			description: "should remove the crossplane security group referencing kcp",
			k8sObjects: []client.Object{
				sgKCP, csg, kcp, kmp, cluster, defaultSecret,
			},
			sg: sgKCP,
		},
		{
			description: "should fail with infrastructureRef not supported",
			k8sObjects: []client.Object{
				sg, csg, kcp, kmp, cluster, defaultSecret,
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "UnsupportedKind",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			expectedError: errors.New("infrastructureRef not supported"),
		},
	}

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}
			fakeOceanClient.MockListClusters = func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test-cluster"),
						},
					},
				}, nil
			}
			fakeOceanClient.MockListLaunchSpecs = func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
				return &oceanaws.ListLaunchSpecsOutput{
					LaunchSpecs: []*oceanaws.LaunchSpec{
						{
							ID:      aws.String("1"),
							Name:    aws.String("vng-test"),
							OceanID: aws.String("o-1"),
							Labels: []*oceanaws.Label{
								{
									Key:   aws.String("kops.k8s.io/instance-group-name"),
									Value: aws.String("test-kops-machine-pool"),
								},
							},
						},
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-yyyy",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
			}

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
				sg:                      tc.sg,
			}

			_, err = reconciliation.reconcileDelete(ctx, tc.sg)
			if tc.expectedError == nil {
				g.Expect(err).ToNot(HaveOccurred())
				deletedSG := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Name: tc.sg.Name,
				}
				err = fakeClient.Get(ctx, key, deletedSG)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

				deletedCSG := &crossec2v1beta1.SecurityGroup{}
				key = client.ObjectKey{
					Name: tc.sg.Name,
				}
				err = fakeClient.Get(ctx, key, deletedCSG)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			} else {
				g.Expect(err).To(MatchError(tc.expectedError))
			}
		})
	}
}

func TestDetachSGFromVNG(t *testing.T) {
	testCases := []struct {
		description            string
		kmp                    *kinfrastructurev1alpha1.KopsMachinePool
		csg                    *crossec2v1beta1.SecurityGroup
		launchSpecs            []*oceanaws.LaunchSpec
		expectedSecurityGroups []string
		errorExpected          error
		updateMockError        bool
	}{
		{
			description: "should detach SG-1 from VNG",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-1",
						"sg-2",
					},
				},
			},
			expectedSecurityGroups: []string{"sg-2"},
		},
		{
			description: "should do nothing if sg is not attached without error",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-2",
					},
				},
			},
			expectedSecurityGroups: []string{"sg-2"},
		},
		{
			description:   "should fail if cant match vng with kmp name",
			kmp:           spotKMP,
			csg:           csg,
			launchSpecs:   []*oceanaws.LaunchSpec{},
			errorExpected: fmt.Errorf("error no vng found with instance group name:"),
		},
		{
			description: "should fail if update return an error",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-1",
						"sg-2",
					},
				},
			},
			updateMockError: true,
			errorExpected:   fmt.Errorf("error updating ocean cluster launch spec:"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}
			if tc.updateMockError {
				fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
					return nil, fmt.Errorf("error")
				}
			} else {
				fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
					g.Expect(updateLaunchSpecInput.LaunchSpec.SecurityGroupIDs).To(Equal(tc.expectedSecurityGroups))
					return &oceanaws.UpdateLaunchSpecOutput{}, nil
				}
			}
			reconciler := SecurityGroupReconciler{
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
			}

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
			}

			err = reconciliation.detachSGFromVNG(ctx, fakeOceanClient, tc.launchSpecs, tc.kmp.Name, csg)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			}
		})
	}
}

func TestDetachSGFromKopsMachinePool(t *testing.T) {
	testCases := []struct {
		description   string
		k8sObjects    []client.Object
		csg           *crossec2v1beta1.SecurityGroup
		kcp           *kcontrolplanev1alpha1.KopsControlPlane
		kmps          []kinfrastructurev1alpha1.KopsMachinePool
		errorExpected error
	}{
		{
			description: "should detach sg from kmp",
			k8sObjects: []client.Object{
				defaultSecret,
			},
			csg: csg,
			kcp: kcp,
			kmps: []kinfrastructurev1alpha1.KopsMachinePool{
				*kmp,
			},
		},
		{
			description: "should detach sg if kmp manager is karpenter",
			k8sObjects: []client.Object{
				defaultSecret,
			},
			csg: csg,
			kcp: kcp,
			kmps: []kinfrastructurev1alpha1.KopsMachinePool{
				*karpenterKMP,
			},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
				return &awsec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []ec2types.LaunchTemplate{
						{
							LaunchTemplateId:   aws.String("lt-xxxx"),
							LaunchTemplateName: aws.String("test-launch-template"),
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("tag:KubernetesCluster"),
									Value: aws.String("test-cluster"),
								},
								{
									Key:   aws.String("tag:Name"),
									Value: aws.String("test-launch-template"),
								},
							},
						},
					},
				}, nil
			}

			reconciler := SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(3),
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			ctx := context.TODO()

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			err = reconciliation.detachSGFromKopsMachinePool(ctx, tc.csg, tc.kmps...)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			}
		})
	}

}

func TestDetachSGFromASG(t *testing.T) {
	testCases := []struct {
		description            string
		k8sObjects             []client.Object
		launchTemplateVersions []ec2types.LaunchTemplateVersion
		asgs                   []autoscalingtypes.AutoScalingGroup
		errorExpected          error
		expectedSecurityGroups []string
	}{
		{
			description: "should detach SecurityGroup from the ASG",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			launchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateId: aws.String("lt-xxxx"),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-1",
								},
							},
						},
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
		},
		{
			description: "should do nothing when SecurityGroup is already detached",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			launchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateId: aws.String("lt-xxxx"),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-1",
								},
							},
						},
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
		},
		{
			description: "should return err when failing to retrieve ASG",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg-2"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			errorExpected: fmt.Errorf("ASG Not Found"),
		},
		{
			description: "should return err when failing to retrieve launch template version",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			errorExpected: fmt.Errorf("Launch Template Not Found"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				launchTemplateVersions := []ec2types.LaunchTemplateVersion{}
				for _, lt := range tc.launchTemplateVersions {
					if *lt.LaunchTemplateId == *params.LaunchTemplateId {
						launchTemplateVersions = append(launchTemplateVersions, lt)
					}
				}
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: launchTemplateVersions,
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				g.Expect(params.LaunchTemplateData.SecurityGroupIds).To(Equal(tc.expectedSecurityGroups))
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				asgs := []autoscalingtypes.AutoScalingGroup{}
				for _, asg := range tc.asgs {
					if *asg.AutoScalingGroupName == params.AutoScalingGroupNames[0] {
						asgs = append(asgs, asg)
					}
				}
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: asgs,
				}, nil
			}
			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				asgClient:               fakeASGClient,
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.detachSGFromASG(ctx, "test-asg", "sg-1")
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))

			}
		})
	}
}

func TestSecurityGroupStatus(t *testing.T) {
	testCases := []struct {
		description                   string
		k8sObjects                    []client.Object
		mockDescribeAutoScalingGroups func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error)
		mockDescribeLaunchTemplates   func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error)
		conditionsToAssert            []*clusterv1beta1.Condition
		errorExpected                 error
		expectedReadiness             bool
	}{
		{
			description: "should successfully patch SecurityGroup",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, csg, defaultSecret,
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupAttachedCondition),
			},
			expectedReadiness: true,
		},
		{
			description: "should mark SG ready condition as false when not available yet",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, defaultSecret, &crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Status: crossec2v1beta1.SecurityGroupStatus{
						ResourceStatus: crossplanev1.ResourceStatus{
							ConditionedStatus: crossplanev1.ConditionedStatus{
								Conditions: []crossplanev1.Condition{
									{
										Type:    "Ready",
										Status:  corev1.ConditionFalse,
										Reason:  "Unavailable",
										Message: "error message",
									},
								},
							},
						},
					},
				},
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha2.SecurityGroupReadyCondition,
					"Unavailable",
					clusterv1beta1.ConditionSeverityError,
					"error message",
				),
			},
			errorExpected: ErrSecurityGroupNotAvailable,
		},
		{
			description: "should mark attach condition as false when failed to attach",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, csg, defaultSecret,
			},
			mockDescribeAutoScalingGroups: func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return nil, errors.New("some error when attaching asg")
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha2.SecurityGroupAttachedCondition,
					securitygroupv1alpha2.SecurityGroupAttachmentFailedReason,
					clusterv1beta1.ConditionSeverityError,
					"some error when attaching asg"),
			},
			errorExpected: errors.New("failed to retrieve AutoScalingGroup"),
		},
		{
			description: "should mark attach condition as false when failed attach to launch template",
			k8sObjects: []client.Object{
				karpenterKMP, cluster, kcp, sg, csg, defaultSecret,
			},
			mockDescribeLaunchTemplates: func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
				return nil, errors.New("some error when retrieving launch template")
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha2.SecurityGroupAttachedCondition,
					securitygroupv1alpha2.SecurityGroupAttachmentFailedReason,
					clusterv1beta1.ConditionSeverityError,
					"some error when retrieving launch template"),
			},
			errorExpected: errors.New("some error when retrieving launch template"),
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).WithStatusSubresource(sg).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}

			if tc.mockDescribeLaunchTemplates == nil {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
					return &awsec2.DescribeLaunchTemplatesOutput{
						LaunchTemplates: []ec2types.LaunchTemplate{
							{
								LaunchTemplateId:   aws.String("lt-xxxx"),
								LaunchTemplateName: aws.String("test-launch-template"),
								Tags: []ec2types.Tag{
									{
										Key:   aws.String("tag:KubernetesCluster"),
										Value: aws.String("test-cluster"),
									},
									{
										Key:   aws.String("tag:Name"),
										Value: aws.String("test-launch-template"),
									},
								},
							},
						},
					}, nil
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplates = tc.mockDescribeLaunchTemplates
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			if tc.mockDescribeAutoScalingGroups == nil {
				fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
					return &awsautoscaling.DescribeAutoScalingGroupsOutput{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{
								AutoScalingGroupName: aws.String("test-asg"),
								LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
									LaunchTemplateId: aws.String("lt-xxxx"),
									Version:          aws.String("1"),
								},
							},
						},
					}, nil
				}
			} else {
				fakeASGClient.MockDescribeAutoScalingGroups = tc.mockDescribeAutoScalingGroups
			}

			reconciler := &SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(5),
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Name: "test-security-group",
				},
			})

			if tc.errorExpected != nil {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			sg = &securitygroupv1alpha2.SecurityGroup{}
			key := client.ObjectKey{
				Name: "test-security-group",
			}
			err = fakeClient.Get(ctx, key, sg)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(sg.Status.Conditions).ToNot(BeNil())
			g.Expect(sg.Status.Ready).To(Equal(tc.expectedReadiness))

			if tc.conditionsToAssert != nil {
				assertConditions(g, sg, tc.conditionsToAssert...)
			}

		})
	}
}

func assertConditions(g *WithT, from conditions.Getter, conditions ...*clusterv1beta1.Condition) {
	for _, condition := range conditions {
		assertCondition(g, from, condition)
	}
}

func assertCondition(g *WithT, from conditions.Getter, condition *clusterv1beta1.Condition) {
	g.Expect(conditions.Has(from, condition.Type)).To(BeTrue())

	if condition.Status == corev1.ConditionTrue {
		conditions.IsTrue(from, condition.Type)
	} else {
		conditionToBeAsserted := conditions.Get(from, condition.Type)
		g.Expect(conditionToBeAsserted.Status).To(Equal(condition.Status))
		g.Expect(conditionToBeAsserted.Severity).To(Equal(condition.Severity))
		g.Expect(conditionToBeAsserted.Reason).To(Equal(condition.Reason))
		if condition.Message != "" {
			g.Expect(conditionToBeAsserted.Message).To(ContainSubstring(condition.Message))
		}
	}
}
