package crossplane

import (
	"context"
	"fmt"
	"testing"

	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	testVPCId  = "vpc-xxxxx"
	testRegion = "us-east-1"
)

func TestCrossPlaneClusterMeshResource(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description":   "should create crossplane clustermesh object",
			"expectedError": false,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			cluster := &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-cluster",
					Annotations: map[string]string{
						"clustermesh.infrastructure.wildlife.io": "testmesh",
					},
					Labels: map[string]string{
						"clusterGroup": "testmesh",
						"environment":  "prod",
						"region":       "us-east-1",
					},
				},
			}
			clusters := []*clustermeshv1beta1.ClusterSpec{}
			clusters = append(clusters, &clustermeshv1beta1.ClusterSpec{
				Name:      "test-cluster",
				Namespace: metav1.NamespaceDefault,
				VPCID:     "vpc-asidjasidiasj",
			})
			sg := NewCrossPlaneClusterMesh(client.ObjectKey{Name: cluster.Labels["clusterGroup"]}, cluster, clusters)
			g.Expect(sg.ObjectMeta.Name).To(ContainSubstring("testmesh"))
		})
	}
}

func TestNewCrossplaneSecurityGroup(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description":  "should return a Crossplane SecurityGroup",
			"ingressRules": []securitygroupv1alpha1.IngressRule{},
		},
		{
			"description": "should return a Crossplane SecurityGroup with multiple Ingresses rules",
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
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ingressRules := tc["ingressRules"].([]securitygroupv1alpha1.IngressRule)
			sg := &securitygroupv1alpha1.SecurityGroup{
				Spec: securitygroupv1alpha1.SecurityGroupSpec{
					IngressRules: ingressRules,
				},
			}
			csg := NewCrossplaneSecurityGroup(sg, &testVPCId, &testRegion)
			g.Expect(csg.Spec.ForProvider.Description).To(Equal(fmt.Sprintf("sg %s managed by provider-crossplane", sg.GetName())))
			g.Expect(len(csg.Spec.ForProvider.Ingress)).To(Equal(len(ingressRules)))
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
							VPCID:       &testVPCId,
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

			ingressRules := tc["ingressRules"].([]securitygroupv1alpha1.IngressRule)
			sg := &securitygroupv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-sg",
				},
				Spec: securitygroupv1alpha1.SecurityGroupSpec{
					IngressRules: ingressRules,
				},
			}
			csg := NewCrossplaneSecurityGroup(sg, &testVPCId, &region)

			err := ManageCrossplaneSecurityGroupResource(ctx, fakeClient, csg)
			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				csg := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-sg",
				}
				err = fakeClient.Get(context.TODO(), key, csg)
				g.Expect(err).To(BeNil())
				g.Expect(csg).NotTo(BeNil())

			}

		})
	}
}

func TestGetSecurityGroupAvailableCondition(t *testing.T) {
	csg := &crossec2v1beta1.SecurityGroup{
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

	testCases := []struct {
		description       string
		conditions        []crossplanev1.Condition
		expectedCondition *crossplanev1.Condition
	}{
		{
			description: "should return ready condition",
			conditions: []crossplanev1.Condition{
				{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				},
			},
			expectedCondition: &crossplanev1.Condition{
				Type:   "Ready",
				Status: corev1.ConditionTrue,
			},
		},
		{
			description: "should return empty when missing ready condition",
			conditions: []crossplanev1.Condition{
				{
					Type:   "Synced",
					Status: corev1.ConditionTrue,
				},
			},
			expectedCondition: nil,
		},
		{
			description: "should return ready condition with multiple conditions",
			conditions: []crossplanev1.Condition{
				{
					Type:   "Synced",
					Status: corev1.ConditionTrue,
				},
				{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				},
			},
			expectedCondition: &crossplanev1.Condition{
				Type:   "Ready",
				Status: corev1.ConditionTrue,
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			csg.Status.Conditions = tc.conditions
			condition := GetSecurityGroupReadyCondition(csg)
			g.Expect(condition).To(Equal(tc.expectedCondition))
		})
	}
}
