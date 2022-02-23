package sgcontroller

import (
	"context"
	"testing"

	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
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
	testVPC = "vpc-xxxxx"
)

func TestGetRegionFromKopsSubnet(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should succeed using subnet with zone",
			"input": kopsapi.ClusterSubnetSpec{
				Zone: "us-east-1d",
			},
			"expected":      "us-east-1",
			"expectedError": false,
		},
		{
			"description": "should succeed using subnet with region",
			"input": kopsapi.ClusterSubnetSpec{
				Region: "us-east-1",
			},
			"expected":      "us-east-1",
			"expectedError": false,
		},
		{
			"description":   "should fail using subnet empty",
			"input":         kopsapi.ClusterSubnetSpec{},
			"expectedError": true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)
	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {

			reconciler := &SecurityGroupReconciler{
				GetVPCIdFromCIDRFactory: func(string, string) (*string, error) {
					return &testVPC, nil
				},
			}

			region, err := reconciler.getRegionFromKopsSubnet(tc["input"].(kopsapi.ClusterSubnetSpec))

			if !tc["expectedError"].(bool) {
				g.Expect(region).ToNot(BeNil())
				g.Expect(err).To(BeNil())
				g.Expect(*region).To(Equal(tc["expected"].(string)))
			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestManageCrossplaneSecurityGroupResource(t *testing.T) {
	region := "us-east-1"
	testCases := []map[string]interface{}{
		{
			"description":   "should create crossplane security group object",
			"k8sObjects":    []client.Object{},
			"ingressRules":  []securitygroupv1alpha1.IngressRule{},
			"expectedError": false,
		},
		{
			"description":  "should update crossplane security group object",
			"ingressRules": []securitygroupv1alpha1.IngressRule{},
			"k8sObjects": []client.Object{
				&crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sg",
						Namespace: metav1.NamespaceDefault,
					},
					Spec: crossec2v1beta1.SecurityGroupSpec{
						ForProvider: crossec2v1beta1.SecurityGroupParameters{
							Region:      &region,
							Description: "test-sg",
							GroupName:   "test-sg",
							VPCID:       &testVPC,
						},
					},
				},
			},
			"expectedError": false,
		},
		{
			"description": "should update crossplane security group object with multiple ingressRules",
			"ingressRules": []securitygroupv1alpha1.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
				{
					IPProtocol: "UDP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			"k8sObjects": []client.Object{
				&crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sg",
						Namespace: metav1.NamespaceDefault,
					},
					Spec: crossec2v1beta1.SecurityGroupSpec{
						ForProvider: crossec2v1beta1.SecurityGroupParameters{
							Region:      &region,
							Description: "test-sg",
							GroupName:   "test-sg",
							VPCID:       &testVPC,
						},
					},
				},
			},
			"expectedError": false,
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()

			k8sObjects := tc["k8sObjects"].([]client.Object)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(k8sObjects...).Build()

			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				log:    ctrl.LoggerFrom(ctx),
			}

			ingressRules := tc["ingressRules"].([]securitygroupv1alpha1.IngressRule)
			sg := reconciler.newCrossplaneSecurityGroup(context.TODO(), "test-sg", metav1.NamespaceDefault, &testVPC, &region, ingressRules)

			err := reconciler.manageCrossplaneSecurityGroupResource(ctx, sg)
			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				crosssg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-sg",
				}
				err = fakeClient.Get(context.TODO(), key, crosssg)
				g.Expect(err).To(BeNil())
				g.Expect(crosssg).NotTo(BeNil())
				g.Expect(crosssg.Spec.ForProvider.Description).NotTo(Equal("test-sg"))
				g.Expect(len(crosssg.Spec.ForProvider.Ingress)).To(Equal(len(ingressRules)))

			}

		})
	}
}

func TestRetrieveKopsMachinePoolInfo(t *testing.T) {
	type expected struct {
		vpcId  string
		region string
	}

	sg := &securitygroupv1alpha1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-security-group",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: securitygroupv1alpha1.SecurityGroupSpec{
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
				Kind:       "KopsMachinePool",
				Name:       "test-kops-machine-pool",
				Namespace:  metav1.NamespaceDefault,
			},
		},
	}

	kmp := &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
		},
	}

	cluster := &clusterv1beta1.Cluster{
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

	kcp := &kcontrolplanev1alpha1.KopsControlPlane{
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

	testCases := []map[string]interface{}{
		{
			"description": "should retrieve region and vpcId",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp,
			},
			"expected": expected{
				testVPC,
				"us-east-1",
			},
			"expectedError": false,
		},
		{
			"description": "should fail without finding KopsControlPlane",
			"k8sObjects": []client.Object{
				kmp, cluster,
			},
			"expectedError": true,
		},
		{
			"description": "should fail without finding Cluster",
			"k8sObjects": []client.Object{
				kmp,
			},
			"expectedError": true,
		},
		{
			"description":   "should fail without finding Cluster",
			"k8sObjects":    []client.Object{},
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

			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				GetVPCIdFromCIDRFactory: func(string, string) (*string, error) {
					return &testVPC, nil
				},
			}
			region, vpcId, err := reconciler.retrieveKopsMachinePoolInfo(ctx, *sg)
			if !tc["expectedError"].(bool) {
				expectedOutput := tc["expected"].(expected)
				g.Expect(err).To(BeNil())
				g.Expect(*region).To(Equal(expectedOutput.region))
				g.Expect(*vpcId).To(Equal(expectedOutput.vpcId))
			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestSecurityGroupReconciler(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should create a Crossplane SecurityGroup",
			"input": &securitygroupv1alpha1.SecurityGroup{
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
			},
			"expectedError": false,
		},
		{
			"description": "should fail without InfrastructureRef defined",
			"input": &securitygroupv1alpha1.SecurityGroup{
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
			"expectedError": true,
		},
		{
			"description": "should fail with InfrastructureRef Kind different from KopsMachinePool",
			"input": &securitygroupv1alpha1.SecurityGroup{
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

	kmp := &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
		},
	}

	cluster := &clusterv1beta1.Cluster{
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

	kcp := &kcontrolplanev1alpha1.KopsControlPlane{
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
						Zone: "us-east-1a",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()

			sg := tc["input"].(*securitygroupv1alpha1.SecurityGroup)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(cluster, sg, kmp, kcp).Build()

			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				GetVPCIdFromCIDRFactory: func(string, string) (*string, error) {
					return &testVPC, nil
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
