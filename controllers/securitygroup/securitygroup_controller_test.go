package sgcontroller

import (
	"context"
	"errors"
	"testing"

	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/provider-crossplane/pkg/aws/ec2/fake"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
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

	csg = &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-security-group",
			Namespace: metav1.NamespaceDefault,
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
			"isErrorExpected": true,
		},
		{
			"description": "should create a SecurityGroup with KopsControlPlane infrastructureRef",
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
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			"isErrorExpected": false,
		},
		{
			"description": "should fail with InfrastructureRef Kind different from KopsMachinePool and KopsControlPlane",
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
			"isErrorExpected": true,
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
				ManageCrossplaneSGFactory: func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
					return crossplane.ManageCrossplaneSecurityGroupResource(ctx, kubeClient, csg)
				},
			}
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-security-group",
				},
			})

			if !tc["isErrorExpected"].(bool) {
				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Name: sg.ObjectMeta.Name,
				}
				err = fakeClient.Get(ctx, key, crosssg)
				g.Expect(err).To(BeNil())
				g.Expect(crosssg).NotTo(BeNil())

			} else {
				g.Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestReconcileKopsControlPlane(t *testing.T) {

	testCases := []struct {
		description     string
		k8sObjects      []client.Object
		isErrorExpected bool
	}{
		{
			description: "should create a Crossplane SecurityGroup",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, &securitygroupv1alpha1.SecurityGroup{
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
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			isErrorExpected: false,
		},
		{
			description: "should fail when not finding Cluster",
			k8sObjects: []client.Object{
				kmp, kcp, sg,
			},
			isErrorExpected: true,
		},
		{
			description: "should fail when not finding KopsControlPlane",
			k8sObjects: []client.Object{
				kmp, cluster, sg,
			},
			isErrorExpected: true,
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

			recorder := record.NewFakeRecorder(5)

			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				Recorder: recorder,
				ManageCrossplaneSGFactory: func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
					return crossplane.ManageCrossplaneSecurityGroupResource(ctx, kubeClient, csg)
				},
			}

			err = reconciler.reconcileKopsControlPlane(ctx, sg, kcp)

			if !tc.isErrorExpected {
				if !errors.Is(err, ErrSecurityGroupNotAvailable) {
					g.Expect(err).To(BeNil())
				}

				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Name: sg.ObjectMeta.Name,
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

	testCases := []struct {
		description     string
		k8sObjects      []client.Object
		isErrorExpected bool
	}{
		{
			description: "should create a Crossplane SecurityGroup",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			isErrorExpected: false,
		},
		{
			description: "should fail when not finding KopsMachinePool",
			k8sObjects: []client.Object{
				cluster, kcp, sg,
			},
			isErrorExpected: true,
		},
		{
			description: "should fail when not finding Cluster",
			k8sObjects: []client.Object{
				kmp, kcp, sg,
			},
			isErrorExpected: true,
		},
		{
			description: "should fail when not finding KopsControlPlane",
			k8sObjects: []client.Object{
				kmp, cluster, sg,
			},
			isErrorExpected: true,
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

			recorder := record.NewFakeRecorder(5)

			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				Recorder: recorder,
				ManageCrossplaneSGFactory: func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
					return crossplane.ManageCrossplaneSecurityGroupResource(ctx, kubeClient, csg)
				},
			}

			err = reconciler.reconcileKopsMachinePool(ctx, sg, kmp)

			if !tc.isErrorExpected {
				if !errors.Is(err, ErrSecurityGroupNotAvailable) {
					g.Expect(err).To(BeNil())
				}

				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Name: sg.ObjectMeta.Name,
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

func TestAttachSGToASG(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should attach SecurityGroup to the ASG",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp, sg,
			},
			"isErrorExpected": false,
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
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeEC2Client.MockModifyLaunchTemplate = func(ctx context.Context, params *awsec2.ModifyLaunchTemplateInput, optFns []func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error) {
				return &awsec2.ModifyLaunchTemplateOutput{
					LaunchTemplate: &ec2types.LaunchTemplate{},
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
							AutoScalingGroupName: aws.String("testASG"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
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

			err = reconciler.attachSGToASG(ctx, fakeEC2Client, fakeASGClient, "asgName", "sg-yyyy")
			if !tc["isErrorExpected"].(bool) {
				g.Expect(err).To(BeNil())

			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestSecurityGroupStatus(t *testing.T) {
	testCases := []struct {
		description                   string
		k8sObjects                    []client.Object
		mockDescribeAutoScalingGroups func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error)
		mockManageCrossplaneSG        func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error
		conditionsToAssert            []*clusterv1beta1.Condition
		isErrorExpected               bool
		expectedReadiness             bool
	}{
		{
			description: "should successfully patch SecurityGroup",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, csg,
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha1.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha1.CrossplaneResourceReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha1.SecurityGroupAttachedCondition),
			},
			isErrorExpected:   false,
			expectedReadiness: true,
		},
		{
			description: "should mark CrossplaneResourceReadyCondition as false when failing to create the CSG",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			mockManageCrossplaneSG: func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
				return errors.New("some error creating CSG")
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.FalseCondition(securitygroupv1alpha1.CrossplaneResourceReadyCondition,
					securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
					clusterv1beta1.ConditionSeverityError,
					"some error creating CSG"),
			},
			isErrorExpected: true,
		},
		{
			description: "should mark SG ready condition as false when not available yet",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, &crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-security-group",
						Namespace: metav1.NamespaceDefault,
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
				conditions.TrueCondition(securitygroupv1alpha1.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha1.SecurityGroupReadyCondition,
					"Unavailable",
					clusterv1beta1.ConditionSeverityError,
					"error message",
				),
			},
			isErrorExpected: false,
		},
		{
			description: "should mark attach condition as false when failed to attach",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, csg,
			},
			mockDescribeAutoScalingGroups: func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return nil, errors.New("some error when attaching asg")
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha1.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha1.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha1.SecurityGroupAttachedCondition,
					securitygroupv1alpha1.SecurityGroupAttachmentFailedReason,
					clusterv1beta1.ConditionSeverityError,
					"some error when attaching asg"),
			},
			isErrorExpected: true,
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
			fakeEC2Client.MockModifyLaunchTemplate = func(ctx context.Context, params *awsec2.ModifyLaunchTemplateInput, optFns []func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error) {
				return &awsec2.ModifyLaunchTemplateOutput{
					LaunchTemplate: &ec2types.LaunchTemplate{},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			if tc.mockDescribeAutoScalingGroups == nil {
				fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
					return &awsautoscaling.DescribeAutoScalingGroupsOutput{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{
								AutoScalingGroupName: aws.String("testASG"),
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
				Client: fakeClient,
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			if tc.mockManageCrossplaneSG == nil {
				reconciler.ManageCrossplaneSGFactory = func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
					return crossplane.ManageCrossplaneSecurityGroupResource(ctx, kubeClient, csg)
				}
			} else {
				reconciler.ManageCrossplaneSGFactory = tc.mockManageCrossplaneSG
			}

			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-security-group",
				},
			})

			if tc.isErrorExpected {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			sg = &securitygroupv1alpha1.SecurityGroup{}
			key := client.ObjectKey{
				Namespace: metav1.NamespaceDefault,
				Name:      "test-security-group",
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
