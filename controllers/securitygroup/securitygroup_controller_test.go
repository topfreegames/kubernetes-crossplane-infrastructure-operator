package sgcontroller

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/provider-crossplane/pkg/aws/ec2/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	sg = &securitygroupv1alpha1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-security-group",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: securitygroupv1alpha1.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha1.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
				Kind:       "KopsMachinePool",
				Name:       "test-kops-machine-pool",
				Namespace:  metav1.NamespaceDefault,
			},
		},
	}

	kmp = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
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
			Name:      "test-kops-control-plane",
		},
		Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
			KopsClusterSpec: kopsapi.ClusterSpec{
				Subnets: []kopsapi.ClusterSubnetSpec{
					{
						Name: "test-subnet",
						CIDR: "0.0.0.0/26",
						Zone: "us-east-1d",
					},
				},
			},
		},
	}
)

func TestSecurityGroupReconciler(t *testing.T) {

	testCases := []map[string]interface{}{
		{
			"description": "should fail without InfrastructureRef defined",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp,
				&securitygroupv1alpha1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-security-group",
						Namespace: metav1.NamespaceDefault,
					},
					Spec: securitygroupv1alpha1.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha1.IngressRule{
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
			"expectedError": true,
		},
		{
			"description": "should fail with InfrastructureRef Kind different from KopsMachinePool",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp,
				&securitygroupv1alpha1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-security-group",
						Namespace: metav1.NamespaceDefault,
					},
					Spec: securitygroupv1alpha1.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha1.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: &corev1.ObjectReference{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "MachinePool",
							Name:       "test-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			"expectedError": true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()

			k8sObjects := tc["k8sObjects"].([]client.Object)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(k8sObjects...).Build()
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
			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-security-group",
				},
			})

			if !tc["expectedError"].(bool) {
				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      sg.ObjectMeta.Name,
				}
				err = fakeClient.Get(ctx, key, crosssg)
				g.Expect(err).To(BeNil())
				g.Expect(crosssg).NotTo(BeNil())

			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestReconcileKopsMachinePool(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should create a Crossplane SecurityGroup",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp, sg,
			},
			"expectedError": false,
		},
		{
			"description": "should fail without finding KopsMachinePool",
			"k8sObjects": []client.Object{
				cluster, kcp, sg,
			},
			"expectedError": true,
		},
		{
			"description": "should fail without finding Cluster",
			"k8sObjects": []client.Object{
				kmp, kcp, sg,
			},
			"expectedError": true,
		},
		{
			"description": "should fail without finding KopsControlPlane",
			"k8sObjects": []client.Object{
				kmp, cluster, sg,
			},
			"expectedError": true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()

			k8sObjects := tc["k8sObjects"].([]client.Object)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(k8sObjects...).Build()
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
			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}
			err := reconciler.ReconcileKopsMachinePool(ctx, sg)

			if !tc["expectedError"].(bool) {
				if !errors.Is(err, ErrSecurityGroupIDNotFound) {
					g.Expect(err).To(BeNil())
				}

				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      sg.ObjectMeta.Name,
				}
				err = fakeClient.Get(ctx, key, crosssg)
				g.Expect(err).To(BeNil())
				g.Expect(crosssg).NotTo(BeNil())

			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestAttachSGToKopsMachinePool(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should create a Crossplane SecurityGroup",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp, sg,
			},
			"expectedError": false,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()

			k8sObjects := tc["k8sObjects"].([]client.Object)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(k8sObjects...).Build()
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
				return nil, nil
			}
			fakeEC2Client.MockModifyLaunchTemplate = func(ctx context.Context, params *awsec2.ModifyLaunchTemplateInput, optFns []func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error) {
				return &awsec2.ModifyLaunchTemplateOutput{
					LaunchTemplate: &ec2types.LaunchTemplate{},
				}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("testASG"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
							},
						},
					},
				}, nil
			}
			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			cfg, err := config.LoadDefaultConfig(ctx,
				config.WithRegion(*aws.String("us-east-1")),
			)
			g.Expect(err).To(BeNil())

			err = reconciler.attachSGToKopsMachinePool(ctx, fakeEC2Client, cfg, "asgName", "sg-yyyy")

			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())

			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}
