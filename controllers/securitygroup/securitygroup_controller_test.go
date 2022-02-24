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

func TestSecurityGroupReconciler(t *testing.T) {

	sg := &securitygroupv1alpha1.SecurityGroup{
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
			"description": "should create a Crossplane SecurityGroup",
			"k8sObjects": []client.Object{
				kmp, cluster, kcp, sg,
			},
			"expectedError": false,
		},
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
		{
			"description": "should fail without finding KopsControlPlane",
			"k8sObjects": []client.Object{
				kmp, cluster, sg,
			},
			"expectedError": true,
		},
		{
			"description": "should fail without finding Cluster",
			"k8sObjects": []client.Object{
				kmp, sg,
			},
			"expectedError": true,
		},
		{
			"description": "should fail without finding Cluster",
			"k8sObjects": []client.Object{
				sg,
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
			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				GetVPCIdFromCIDRFactory: func(*string, string) (*string, error) {
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
