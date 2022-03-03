package crossplane

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	testVPC = "vpc-xxxxx"
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
			ctx := context.TODO()

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
			clusterRefList := []*v1.ObjectReference{}
			clusterRef := &v1.ObjectReference{
				APIVersion: cluster.TypeMeta.APIVersion,
				Kind:       cluster.TypeMeta.Kind,
				Name:       cluster.ObjectMeta.Name,
				Namespace:  cluster.ObjectMeta.Namespace,
			}
			clusterRefList = append(clusterRefList, clusterRef)
			sg := NewCrossPlaneClusterMesh(ctx, client.ObjectKey{Name: cluster.Labels["clusterGroup"]}, cluster, clusterRefList)
			g.Expect(sg.ObjectMeta.Name).To(ContainSubstring("testmesh"))
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

			ingressRules := tc["ingressRules"].([]securitygroupv1alpha1.IngressRule)
			sg := NewCrossplaneSecurityGroup(context.TODO(), "test-sg", metav1.NamespaceDefault, &testVPC, &region, ingressRules)

			err := ManageCrossplaneSecurityGroupResource(ctx, fakeClient, sg)
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
