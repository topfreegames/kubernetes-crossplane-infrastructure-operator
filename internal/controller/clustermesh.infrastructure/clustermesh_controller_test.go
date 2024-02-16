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

package clustermesh

import (
	"context"
	"fmt"
	"testing"

	clustermeshv1alpha1 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/clustermesh.infrastructure/v1alpha1"
	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	crossec2v1alpha1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1alpha1"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	fakeec2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2/fake"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kopsapi "k8s.io/kops/pkg/apis/kops"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	vpcPeeringConnectionAPIVersion = "ec2.aws.crossplane.io/v1alpha1"
)

var defaultSecret = &corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "default",
		Namespace: "kubernetes-kops-operator-system",
	},
	Data: map[string][]byte{
		"AccessKeyID":     []byte("AK"),
		"SecretAccessKey": []byte("SAK"),
	},
}

var defaultKcp = &kcontrolplanev1alpha1.KopsControlPlane{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "kcp-test",
		Namespace: metav1.NamespaceDefault,
	},
	Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
		KopsClusterSpec: kopsapi.ClusterSpec{
			Networking: kopsapi.NetworkingSpec{
				Subnets: []kopsapi.ClusterSubnetSpec{
					{
						Name: "us-east-1a",
						Zone: "us-east-1a",
					},
				},
			},
		},
		IdentityRef: kcontrolplanev1alpha1.IdentityRefSpec{
			Name:      "default",
			Namespace: "kubernetes-kops-operator-system",
		},
	},
}

func TestClusterMeshReconciler(t *testing.T) {
	testCases := []struct {
		description                    string
		k8sObjects                     []client.Object
		expectedClusterMesh            []clustermeshv1alpha1.ClusterMesh
		expectedClusterMeshToBeDeleted []string
	}{
		{
			description: "should do nothing if cluster don't have the clustermesh annotation",
			k8sObjects: []client.Object{
				defaultSecret,
				defaultKcp,
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-cluster",
						Labels: map[string]string{
							"clusterGroup": "test-mesh",
							"environment":  "prod",
							"region":       "us-east-1",
						},
					},
					Spec: clusterv1beta1.ClusterSpec{
						ControlPlaneRef: &corev1.ObjectReference{
							Kind:      "KopsControlPlane",
							Namespace: metav1.NamespaceDefault,
							Name:      "kcp-test",
						},
					},
				},
			},
		},
		{
			description: "should do nothing if cluster don't have the label with clusterGroup",
			k8sObjects: []client.Object{
				defaultSecret,
				defaultKcp,
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-cluster",
						Annotations: map[string]string{
							"clustermesh.infrastructure.wildlife.io": "testmesh",
						},
						Labels: map[string]string{
							"environment": "prod",
							"region":      "us-east-1",
						},
					},
					Spec: clusterv1beta1.ClusterSpec{
						ControlPlaneRef: &corev1.ObjectReference{
							Kind:      "KopsControlPlane",
							Namespace: metav1.NamespaceDefault,
							Name:      "kcp-test",
						},
					},
				},
			},
		},
		{
			description: "should create clustermesh when cluster has correct annotations and is the only cluster in mesh",
			k8sObjects: []client.Object{
				defaultSecret,
				defaultKcp,
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-cluster",
						Annotations: map[string]string{
							"clustermesh.infrastructure.wildlife.io": "testmesh",
						},
						Labels: map[string]string{
							"environment":  "prod",
							"region":       "us-east-1",
							"clusterGroup": "test-mesh",
						},
					},
					Spec: clusterv1beta1.ClusterSpec{
						ControlPlaneRef: &corev1.ObjectReference{
							Kind:      "KopsControlPlane",
							Namespace: metav1.NamespaceDefault,
							Name:      "kcp-test",
						},
					},
				},
			},
			expectedClusterMesh: []clustermeshv1alpha1.ClusterMesh{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-mesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name:   "test-cluster",
								VPCID:  "xxx",
								Region: "yyy",
							},
						},
					},
				},
			},
		},
		{
			description: "should delete clustermesh when cluster belong to a clustermesh, but don't have the correct annotations",
			k8sObjects: []client.Object{
				defaultSecret,
				defaultKcp,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: getClusterMeshSecurityGroupName("test-cluster"),
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
				},
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-cluster",
						Labels: map[string]string{
							"environment":  "prod",
							"region":       "us-east-1",
							"clusterGroup": "test-mesh",
						},
					},
					Spec: clusterv1beta1.ClusterSpec{
						ControlPlaneRef: &corev1.ObjectReference{
							Kind:      "KopsControlPlane",
							Namespace: metav1.NamespaceDefault,
							Name:      "kcp-test",
						},
					},
				},
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-mesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name:   "test-cluster",
								VPCID:  "xxx",
								Region: "xxx",
							},
						},
					},
				},
			},
			expectedClusterMeshToBeDeleted: []string{
				"test-mesh",
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
				PopulateClusterSpecFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1alpha1.ClusterSpec, error) {
					return &clustermeshv1alpha1.ClusterSpec{
						Name:   cluster.Name,
						VPCID:  "xxx",
						Region: "yyy",
					}, nil
				},
				ReconcilePeeringsFactory: ReconcilePeerings,
				ReconcileRoutesFactory:   ReconcileRoutes,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return &fakeec2.MockEC2Client{}
				},
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-cluster",
				},
			})
			g.Expect(err).NotTo(HaveOccurred())

			for _, clustermesh := range tc.expectedClusterMesh {
				ccm := &clustermeshv1alpha1.ClusterMesh{}
				key := client.ObjectKey{
					Name: clustermesh.Name,
				}
				err = fakeClient.Get(ctx, key, ccm)
				g.Expect(err).To(BeNil())
				g.Expect(ccm.Spec).To(BeEquivalentTo(clustermesh.Spec))
			}
			for _, clusterMeshName := range tc.expectedClusterMeshToBeDeleted {
				ccm := &clustermeshv1alpha1.ClusterMesh{}
				key := client.ObjectKey{
					Name: clusterMeshName,
				}
				err = fakeClient.Get(ctx, key, ccm)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

func TestIsClusterMeshEnabled(t *testing.T) {
	testCases := []struct {
		description    string
		cluster        clusterv1beta1.Cluster
		outputExpected bool
	}{
		{
			description: "should return that clustermesh is enabled",
			cluster: clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"clustermesh.infrastructure.wildlife.io": "true",
					},
					Labels: map[string]string{
						"clusterGroup": "test-mesh",
					},
				},
			},
			outputExpected: true,
		},
		{
			description: "should return that clustermesh isn't enabled without annotation",
			cluster: clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"clusterGroup": "test-mesh",
					},
				},
			},
			outputExpected: false,
		},
		{
			description: "should return that clustermesh isn't enabled without label",
			cluster: clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"clustermesh.infrastructure.wildlife.io": "true",
					},
				},
			},
			outputExpected: false,
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			clusterEnabled := isClusterMeshEnabled(tc.cluster)

			g.Expect(clusterEnabled).To(Equal(tc.outputExpected))
		})
	}

}

func TestPopulateClusterSpec(t *testing.T) {

	type mockDescribeVPCOutput struct {
		*awsec2.DescribeVpcsOutput
		error
	}

	type mockDescribeRouteTableOutput struct {
		*awsec2.DescribeRouteTablesOutput
		error
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description                  string
		k8sObjects                   []client.Object
		cluster                      *clusterv1beta1.Cluster
		mockDescribeVPCOutput        *mockDescribeVPCOutput
		mockDescribeRouteTableOutput *mockDescribeRouteTableOutput
		errorValidation              func(error) bool
		expectedOutput               *clustermeshv1alpha1.ClusterSpec
	}{
		{
			description: "should return the clSpec as expected",
			k8sObjects: []client.Object{
				&kcontrolplanev1alpha1.KopsControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kcp-test",
						Namespace: metav1.NamespaceDefault,
					},
					Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
						KopsClusterSpec: kopsapi.ClusterSpec{
							Networking: kopsapi.NetworkingSpec{
								Subnets: []kopsapi.ClusterSubnetSpec{
									{
										Name: "us-east-1a",
										Zone: "us-east-1a",
									},
								},
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: clusterv1beta1.ClusterSpec{
					ControlPlaneRef: &corev1.ObjectReference{
						Kind:      "KopsControlPlane",
						Namespace: metav1.NamespaceDefault,
						Name:      "kcp-test",
					},
				},
			},
			mockDescribeVPCOutput: &mockDescribeVPCOutput{
				&awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("xxx"),
						},
					},
				}, nil,
			},
			mockDescribeRouteTableOutput: &mockDescribeRouteTableOutput{
				&awsec2.DescribeRouteTablesOutput{
					RouteTables: []ec2types.RouteTable{
						{
							RouteTableId: aws.String("rt-xxxxx"),
						},
					},
				}, nil,
			},
			expectedOutput: &clustermeshv1alpha1.ClusterSpec{
				Name:   "cluster-test",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
		},
		{
			description: "should fail when VPC isn't found",
			k8sObjects: []client.Object{
				&kcontrolplanev1alpha1.KopsControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kcp-test",
						Namespace: metav1.NamespaceDefault,
					},
					Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
						KopsClusterSpec: kopsapi.ClusterSpec{
							Networking: kopsapi.NetworkingSpec{
								Subnets: []kopsapi.ClusterSubnetSpec{
									{
										Name: "us-east-1a",
										Zone: "us-east-1a",
									},
								},
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: clusterv1beta1.ClusterSpec{
					ControlPlaneRef: &corev1.ObjectReference{
						Kind:      "KopsControlPlane",
						Namespace: metav1.NamespaceDefault,
						Name:      "kcp-test",
					},
				},
			},
			mockDescribeVPCOutput: &mockDescribeVPCOutput{
				&awsec2.DescribeVpcsOutput{}, nil,
			},
			mockDescribeRouteTableOutput: &mockDescribeRouteTableOutput{
				&awsec2.DescribeRouteTablesOutput{}, nil,
			},
			errorValidation: func(err error) bool {
				return g.Expect(err.Error()).Should(ContainSubstring("VPC Not Found"))
			},
		},
	}

	err := kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			fakeEC2Client := &fakeec2.MockEC2Client{
				MockDescribeVpcs: func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
					return tc.mockDescribeVPCOutput.DescribeVpcsOutput, tc.mockDescribeVPCOutput.error
				},
				MockDescribeRouteTables: func(ctx context.Context, params *awsec2.DescribeRouteTablesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeRouteTablesOutput, error) {
					return tc.mockDescribeRouteTableOutput.DescribeRouteTablesOutput, tc.mockDescribeRouteTableOutput.error
				},
			}

			kcpKey := client.ObjectKeyFromObject(defaultKcp)

			kcp := &kcontrolplanev1alpha1.KopsControlPlane{}

			_ = fakeClient.Get(context.TODO(), kcpKey, kcp)

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				kcp:                   kcp,
				ec2Client:             reconciler.NewEC2ClientFactory(aws.Config{}),
			}

			clSpec, err := PopulateClusterSpec(reconciliation, context.TODO(), tc.cluster)
			if tc.errorValidation != nil {
				g.Expect(tc.errorValidation(err)).To(BeTrue())
			} else {
				g.Expect(clSpec)
			}
		})
	}

}

func TestIsClusterBelongToAnyMesh(t *testing.T) {

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description     string
		k8sObjects      []client.Object
		errorValidation func(error) bool
		clustermeshName string
		expectedOutput  bool
	}{
		{
			description:    "should return false when having no clustermeshes",
			expectedOutput: false,
		},
		{
			description: "should return true when the cluster is part of a clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "A",
							},
						},
					},
				},
			},
			clustermeshName: "test-clustermesh",
			expectedOutput:  true,
		},
		{
			description: "should return false when the cluster isn't part of any clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh-A",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "B",
							},
							{
								Name: "C",
							},
						},
					},
				},
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh-B",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "D",
							},
							{
								Name: "E",
							},
						},
					},
				},
			},
			expectedOutput: false,
		},
	}

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
			}

			belong, meshName, err := reconciliation.isClusterBelongToAnyMesh("A")
			if tc.errorValidation != nil {
				g.Expect(tc.errorValidation(err)).To(BeTrue())
			} else {
				g.Expect(belong).To(BeEquivalentTo(tc.expectedOutput))
				g.Expect(meshName).To(BeEquivalentTo(tc.clustermeshName))
			}
		})
	}
}

func TestReconcileNormal(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description       string
		k8sObjects        []client.Object
		cluster           *clusterv1beta1.Cluster
		clustermesh       *clustermeshv1alpha1.ClusterMesh
		expectedOutput    *clustermeshv1alpha1.ClusterMesh
		shouldNotValidate bool
	}{
		{
			description: "should create a clustermesh with the cluster A in the spec",
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterMesh",
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-clustermesh",
					ResourceVersion: "1",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
			},
			shouldNotValidate: true,
		},
		{
			description: "should add the cluster B in the spec of an already create clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "A",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "B",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
						{
							Name: "B",
						},
					},
				},
			},
		},
		{
			description: "should not add cluster B in the spec if it already exists in clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "B",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "B",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
					},
				},
			},
		},
		{
			description: "should add the cluster in the spec of an already create empty clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
			},
		},
	}

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
				PopulateClusterSpecFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1alpha1.ClusterSpec, error) {
					return &clustermeshv1alpha1.ClusterSpec{
						Name: cluster.Name,
					}, nil
				},
				ReconcilePeeringsFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error {
					return nil
				},
				ReconcileRoutesFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) (ctrl.Result, error) {
					return ctrl.Result{}, nil
				},
				ReconcileSecurityGroupsFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error {
					return nil
				},
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				cluster:               tc.cluster,
				log:                   ctrl.LoggerFrom(ctx),
				clustermesh:           &clustermeshv1alpha1.ClusterMesh{},
			}

			_, _ = reconciliation.reconcileNormal(ctx)
			clustermesh := &clustermeshv1alpha1.ClusterMesh{}
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-clustermesh"}, clustermesh)
			g.Expect(clustermesh.GetName()).To(BeEquivalentTo(tc.expectedOutput.GetName()))
			g.Expect(clustermesh.Spec).To(BeEquivalentTo(tc.expectedOutput.Spec))
			if tc.shouldNotValidate {
				g.Expect(clustermesh.Status.Conditions).To(BeEmpty())
			}
		})
	}
}

func TestReconcileDelete(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description                          string
		k8sObjects                           []client.Object
		cluster                              *clusterv1beta1.Cluster
		clustermesh                          *clustermeshv1alpha1.ClusterMesh
		expectedVpcPeeringConnections        []string
		expectedSecurityGroup                []string
		shouldBeDeletedVpcPeeringConnections []string
		shouldBeDeletedSecurityGroup         []string
		expectedOutput                       *clustermeshv1alpha1.ClusterMesh
	}{
		{
			description: "should remove the cluster A from clustermesh spec",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "A",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "A",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
		},
		{
			description: "should not remove anything when the cluster don't exist in the clustermesh spec",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "B",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-B-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "B",
								Namespace:  "kubernetes-B",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "A",
								Namespace:  "kubernetes-A",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
					},
				},
			},
		},
		{
			description: "should remove cluster A in the first position",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "A",
							},
							{
								Name: "B",
							},
							{
								Name: "C",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "A",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
						{
							Name: "C",
						},
					},
				},
			},
		},
		{
			description: "should remove cluster A in the last position",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "C",
							},
							{
								Name: "B",
							},
							{
								Name: "A",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "A",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "C",
						},
						{
							Name: "B",
						},
					},
				},
			},
		},
		{
			description: "should remove the security group of the cluster when cluster is deleted",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "A",
							},
							{
								Name: "B",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "A",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-B-sg",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						InfrastructureRef: []*corev1.ObjectReference{
							{
								Name:       "B",
								Kind:       "KopsControlPlane",
								APIVersion: "controlplane.x-k8s.io/v1alpha1",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
					},
				},
			},
		},
		{
			description: "should remove vpcpeerings related to A cluster when it's deleted",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "B",
							},
							{
								Name: "A",
							},
							{
								Name: "C",
							},
						},
					},
					Status: clustermeshv1alpha1.ClusterMeshStatus{
						CrossplanePeeringRef: []*corev1.ObjectReference{
							{
								Name:       "A-B",
								APIVersion: vpcPeeringConnectionAPIVersion,
								Kind:       "VPCPeeringConnection",
							},
							{
								Name:       "A-C",
								APIVersion: vpcPeeringConnectionAPIVersion,
								Kind:       "VPCPeeringConnection",
							},
							{
								Name:       "B-C",
								APIVersion: vpcPeeringConnectionAPIVersion,
								Kind:       "VPCPeeringConnection",
							},
						},
					},
				},
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "B-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
						{
							Name: "A",
						},
						{
							Name: "C",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "A-C",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "B-C",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
						{
							Name: "C",
						},
					},
				},
			},
			expectedVpcPeeringConnections:        []string{"B-C"},
			shouldBeDeletedVpcPeeringConnections: []string{"A-C", "A-B"},
		},
		{
			description: "should remove last vpcpeering of the clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "A",
							},
							{
								Name: "B",
							},
						},
					},
					Status: clustermeshv1alpha1.ClusterMeshStatus{
						CrossplanePeeringRef: []*corev1.ObjectReference{
							{
								Name:       "A-B",
								APIVersion: vpcPeeringConnectionAPIVersion,
								Kind:       "VPCPeeringConnection",
							},
						},
					},
				},
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
						{
							Name: "B",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
					},
				},
			},
			shouldBeDeletedVpcPeeringConnections: []string{"A-B"},
		},
		{
			description: "should remove crossplaneRef related to A cluster when it's deleted",
			k8sObjects: []client.Object{
				&clustermeshv1alpha1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1alpha1.ClusterMeshSpec{
						Clusters: []*clustermeshv1alpha1.ClusterSpec{
							{
								Name: "B",
							},
							{
								Name: "A",
							},
							{
								Name: "C",
							},
						},
					},
					Status: clustermeshv1alpha1.ClusterMeshStatus{
						CrossplaneSecurityGroupRef: []*corev1.ObjectReference{
							{
								Name:       "clustermesh-A-sg",
								APIVersion: "ec2.aws.crossplane.io/v1alpha1",
								Kind:       "SecurityGroup",
							},
							{
								Name:       "clustermesh-B-sg",
								APIVersion: "ec2.aws.crossplane.io/v1alpha1",
								Kind:       "SecurityGroup",
							},
							{
								Name:       "clustermesh-C-sg",
								APIVersion: "ec2.aws.crossplane.io/v1alpha1",
								Kind:       "SecurityGroup",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "SecurityGroup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "SecurityGroup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-B-sg",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "SecurityGroup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-C-sg",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
						{
							Name: "A",
						},
						{
							Name: "C",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplaneSecurityGroupRef: []*corev1.ObjectReference{
						{
							Name:       "clustermesh-A-sg",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "SecurityGroup",
						},
						{
							Name:       "clustermesh-B-sg",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "SecurityGroup",
						},
						{
							Name:       "clustermesh-C-sg",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "SecurityGroup",
						},
					},
				},
			},
			expectedOutput: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "B",
						},
						{
							Name: "C",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplaneSecurityGroupRef: []*corev1.ObjectReference{
						{
							Name:       "clustermesh-B-sg",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "SecurityGroup",
						},
						{
							Name:       "clustermesh-C-sg",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "SecurityGroup",
						},
					},
				},
			},
			expectedSecurityGroup:        []string{"clustermesh-B-sg", "clustermesh-C-sg"},
			shouldBeDeletedSecurityGroup: []string{"clustermesh-A-sg"},
		},
	}

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				clustermesh:           tc.clustermesh,
				cluster:               tc.cluster,
				log:                   ctrl.LoggerFrom(ctx),
			}

			err = reconciliation.reconcileDelete(ctx)
			g.Expect(err).ToNot(HaveOccurred())

			clustermesh := &clustermeshv1alpha1.ClusterMesh{}
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-clustermesh"}, clustermesh)
			if tc.expectedOutput != nil {
				g.Expect(tc.clustermesh.GetName()).To(BeEquivalentTo(tc.expectedOutput.GetName()))
				g.Expect(tc.clustermesh.Spec).To(BeEquivalentTo(tc.expectedOutput.Spec))
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

			for _, vpcPeeringConnectionName := range tc.expectedVpcPeeringConnections {
				vpcPeeringConnection := &crossec2v1alpha1.VPCPeeringConnection{}
				key := client.ObjectKey{
					Name: vpcPeeringConnectionName,
				}
				err = fakeClient.Get(ctx, key, vpcPeeringConnection)
				g.Expect(err).To(BeNil())
			}

			for _, vpcPeeringConnectionName := range tc.shouldBeDeletedVpcPeeringConnections {
				vpcPeeringConnection := &crossec2v1alpha1.VPCPeeringConnection{}
				key := client.ObjectKey{
					Name: vpcPeeringConnectionName,
				}
				err = fakeClient.Get(ctx, key, vpcPeeringConnection)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

			for _, securityGroupName := range tc.expectedSecurityGroup {
				securityGroup := &securitygroupv1alpha2.SecurityGroup{}
				key := client.ObjectKey{
					Name: securityGroupName,
				}
				err = fakeClient.Get(ctx, key, securityGroup)
				g.Expect(err).To(BeNil())
			}

			for _, securityGroupName := range tc.shouldBeDeletedSecurityGroup {
				securityGroup := &securitygroupv1alpha2.SecurityGroup{}
				key := client.ObjectKey{
					Name: securityGroupName,
				}
				err = fakeClient.Get(ctx, key, securityGroup)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

		})
	}
}

func TestReconcilePeerings(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description                   string
		k8sObjects                    []client.Object
		clSpec                        *clustermeshv1alpha1.ClusterSpec
		clustermesh                   *clustermeshv1alpha1.ClusterMesh
		expectedVpcPeeringConnections []string
	}{
		{
			description: "should not create a vpcpeering with only one cluster",
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				Name: "A",
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
			},
		},
		{
			description: "should create vpcpeeringconnection A-B, A-C, not B-C",
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				Name: "A",
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
						{
							Name: "B",
						},
						{
							Name: "C",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"A-B", "A-C"},
		},
		{
			description: "should not create a vpcpeering when it already exists",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				Name: "A",
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
						{
							Name: "B",
						},
						{
							Name: "C",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: "ec2.aws.wildife.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"A-B", "A-C"},
		},
		{
			description: "should not create a vpcpeering when it already exists inverted",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "B-A",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				Name: "A",
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "A",
						},
						{
							Name: "B",
						},
						{
							Name: "C",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "B-A",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"B-A", "A-C"},
		},
		{
			description: "should create correctly with different spec orders",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				Name: "A",
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name: "C",
						},
						{
							Name: "B",
						},
						{
							Name: "A",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"A-B", "A-C"},
		},
	}

	err := crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
				ReconcilePeeringsFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error {
					return nil
				},
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				clustermesh:           tc.clustermesh,
			}

			_ = ReconcilePeerings(reconciliation, ctx, tc.clSpec)
			for _, vpcPeeringConnectionName := range tc.expectedVpcPeeringConnections {
				vpcPeeringConnection := &crossec2v1alpha1.VPCPeeringConnection{}
				key := client.ObjectKey{
					Name: vpcPeeringConnectionName,
				}
				err = fakeClient.Get(ctx, key, vpcPeeringConnection)
				g.Expect(err).To(BeNil())
			}

		})
	}
}

func TestReconcileRoutes(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	clustermesh := clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterMesh",
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
		},
		Spec: clustermeshv1alpha1.ClusterMeshSpec{
			Clusters: []*clustermeshv1alpha1.ClusterSpec{
				{
					Name:      "A",
					Namespace: "A",
				},
				{
					Name:      "B",
					Namespace: "B",
				},
			},
		},
	}

	vpcPeeringConnection := crossec2v1alpha1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vpcPeeringConnectionAPIVersion,
			Kind:       "VPCPeeringConnection",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "a-b",
			Annotations: map[string]string{
				"crossplane.io/external-name": "pcx-xxxx",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
					Kind:       "ClusterMesh",
					Name:       clustermesh.Name,
				},
			},
		},
		Status: crossec2v1alpha1.VPCPeeringConnectionStatus{
			ResourceStatus: v1.ResourceStatus{
				ConditionedStatus: *v1.NewConditionedStatus(v1.Available(), v1.ReconcileSuccess()),
			},
			AtProvider: crossec2v1alpha1.VPCPeeringConnectionObservation{
				AccepterVPCInfo: &crossec2v1alpha1.VPCPeeringConnectionVPCInfo{
					CIDRBlock: aws.String("aaaaa"),
				},
				RequesterVPCInfo: &crossec2v1alpha1.VPCPeeringConnectionVPCInfo{
					CIDRBlock: aws.String("bbbbb"),
				},
			},
		},
	}

	testCases := []struct {
		description                  string
		k8sObjects                   []client.Object
		clSpec                       *clustermeshv1alpha1.ClusterSpec
		shouldCheckRoute             bool
		shouldCreateRoute            bool
		shouldCheckClustermeshStatus bool
	}{
		{
			description: "should create route if does not exists and vpcPeering is ready. also the current cluster is the accepter on vpc peering",
			k8sObjects: []client.Object{
				&clustermesh,
				&vpcPeeringConnection,
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				VPCID:  "vpc-xxxxx",
				Name:   "cluster-a",
				Region: "us-east-1",
				CIDR:   "aaaaa",
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			shouldCreateRoute: true,
		},
		{
			description: "should create route if does not exists and vpcPeering is ready. also the current cluster is the requester on vpc peering",
			k8sObjects: []client.Object{
				&clustermesh,
				&vpcPeeringConnection,
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				VPCID:  "vpc-xxxxx",
				Name:   "cluster-a",
				Region: "us-east-1",
				CIDR:   "bbbbb",
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			shouldCreateRoute: true,
		},
		{
			description: "should not create routes and return without error if vpcPeering is not ready",
			k8sObjects: []client.Object{
				&clustermesh,
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "a-b",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
								Kind:       "ClusterMesh",
								Name:       clustermesh.Name,
							},
						},
					},
					Status: crossec2v1alpha1.VPCPeeringConnectionStatus{
						ResourceStatus: v1.ResourceStatus{
							ConditionedStatus: *v1.NewConditionedStatus(v1.Unavailable()),
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				VPCID:  "vpc-xxxxx",
				Name:   "cluster-a",
				Region: "us-east-1",
				CIDR:   "bbbbb",
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			shouldCreateRoute: false,
		},
		{
			description: "should not create routes if cluster don't belong to any vpcPeering",
			k8sObjects: []client.Object{
				&clustermesh,
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "a-b",
						Annotations: map[string]string{
							"crossplane.io/external-name": "pcx-xxxx",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
								Kind:       "ClusterMesh",
								Name:       clustermesh.Name,
							},
						},
					},
					Status: crossec2v1alpha1.VPCPeeringConnectionStatus{
						ResourceStatus: v1.ResourceStatus{
							ConditionedStatus: *v1.NewConditionedStatus(v1.Available()),
						},
						AtProvider: crossec2v1alpha1.VPCPeeringConnectionObservation{
							AccepterVPCInfo: &crossec2v1alpha1.VPCPeeringConnectionVPCInfo{
								CIDRBlock: aws.String("ccccc"),
							},
							RequesterVPCInfo: &crossec2v1alpha1.VPCPeeringConnectionVPCInfo{
								CIDRBlock: aws.String("bbbbb"),
							},
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				VPCID:  "vpc-xxxxx",
				Name:   "cluster-a",
				Region: "us-east-1",
				CIDR:   "aaaaa",
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			shouldCreateRoute: false,
		},
		{
			description: "should create routes even if not all routes are created",
			k8sObjects: []client.Object{
				&clustermesh,
				&vpcPeeringConnection,
				&crossec2v1alpha1.Route{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "Route",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rt-xxxx-pcx-xxxx",
						UID:  "xxx",
					},
					Spec: crossec2v1alpha1.RouteSpec{
						ForProvider: crossec2v1alpha1.RouteParameters{
							DestinationCIDRBlock: aws.String("bbbbb"),
							Region:               "us-east-1",
							CustomRouteParameters: crossec2v1alpha1.CustomRouteParameters{
								VPCPeeringConnectionID: aws.String("pcx-xxxx"),
								RouteTableID:           aws.String("rt-xxxx"),
							},
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				VPCID:  "vpc-xxxxx",
				Name:   "cluster-a",
				Region: "us-east-1",
				CIDR:   "aaaaa",
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			shouldCreateRoute: true,
		},
		{
			description: "should add already-created routes to the ClusterMesh status",
			k8sObjects: []client.Object{
				&clustermesh,
				&vpcPeeringConnection,
				&crossec2v1alpha1.Route{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "Route",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rt-xxxx-pcx-xxxx",
						UID:  "xxx",
					},
					Spec: crossec2v1alpha1.RouteSpec{
						ForProvider: crossec2v1alpha1.RouteParameters{
							DestinationCIDRBlock: aws.String("bbbbb"),
							Region:               "us-east-1",
							CustomRouteParameters: crossec2v1alpha1.CustomRouteParameters{
								VPCPeeringConnectionID: aws.String("pcx-xxxx"),
								RouteTableID:           aws.String("rt-xxxx"),
							},
						},
					},
				},
				&crossec2v1alpha1.Route{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "Route",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rt-zzzz-pcx-zzzz",
						UID:  "zzz",
					},
					Spec: crossec2v1alpha1.RouteSpec{
						ForProvider: crossec2v1alpha1.RouteParameters{
							DestinationCIDRBlock: aws.String("ccccc"),
							Region:               "us-east-1",
							CustomRouteParameters: crossec2v1alpha1.CustomRouteParameters{
								VPCPeeringConnectionID: aws.String("pcx-zzzz"),
								RouteTableID:           aws.String("rt-zzzz"),
							},
						},
					},
				},
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "VPCPeeringConnection",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "c-a",
						Annotations: map[string]string{
							"crossplane.io/external-name": "pcx-zzzz",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
								Kind:       "ClusterMesh",
								Name:       clustermesh.Name,
							},
						},
					},
					Status: crossec2v1alpha1.VPCPeeringConnectionStatus{
						ResourceStatus: v1.ResourceStatus{
							ConditionedStatus: *v1.NewConditionedStatus(v1.Available(), v1.ReconcileSuccess()),
						},
						AtProvider: crossec2v1alpha1.VPCPeeringConnectionObservation{
							AccepterVPCInfo: &crossec2v1alpha1.VPCPeeringConnectionVPCInfo{
								CIDRBlock: aws.String("ccccc"),
							},
							RequesterVPCInfo: &crossec2v1alpha1.VPCPeeringConnectionVPCInfo{
								CIDRBlock: aws.String("aaaaa"),
							},
						},
					},
				},
			},
			clSpec: &clustermeshv1alpha1.ClusterSpec{
				VPCID:  "vpc-xxxxx",
				Name:   "cluster-a",
				Region: "us-east-1",
				CIDR:   "aaaaa",
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			shouldCheckRoute:             true,
			shouldCreateRoute:            true,
			shouldCheckClustermeshStatus: true,
		},
	}

	err := crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				log:                   ctrl.LoggerFrom(ctx),
				clustermesh:           &clustermesh,
			}

			_, err = ReconcileRoutes(reconciliation, ctx, tc.clSpec)
			g.Expect(err).To(BeNil())

			routes := &crossec2v1alpha1.RouteList{}
			err = fakeClient.List(ctx, routes)
			g.Expect(err).To(BeNil())
			if !tc.shouldCreateRoute {
				g.Expect(routes.Items).To(BeEmpty())
			} else {
				g.Expect(routes.Items).To(Not(BeEmpty()))
			}
			vpcPeerings := &crossec2v1alpha1.VPCPeeringConnectionList{}
			err = fakeClient.List(ctx, vpcPeerings)
			g.Expect(err).To(BeNil())
			g.Expect(vpcPeerings.Items).To(Not(BeEmpty()))
			var vpcPeeringsIds []*string
			var vpcPeeringsCIDRBlock []*string
			for _, vpcPeering := range vpcPeerings.Items {
				vpcPeeringsIds = append(vpcPeeringsIds, aws.String(vpcPeering.Annotations["crossplane.io/external-name"]))
				if vpcPeering.Status.AtProvider.RequesterVPCInfo != nil {
					vpcPeeringsCIDRBlock = append(vpcPeeringsCIDRBlock, vpcPeering.Status.AtProvider.RequesterVPCInfo.CIDRBlock)
				}
				if vpcPeering.Status.AtProvider.AccepterVPCInfo != nil {
					vpcPeeringsCIDRBlock = append(vpcPeeringsCIDRBlock, vpcPeering.Status.AtProvider.AccepterVPCInfo.CIDRBlock)
				}
			}

			for _, route := range routes.Items {
				g.Expect(aws.ToString(route.Spec.ForProvider.RouteTableID)).To(BeElementOf(tc.clSpec.RouteTableIDs))
				if len(vpcPeerings.Items) > 1 {
					g.Expect(route.Spec.ForProvider.VPCPeeringConnectionID).To(BeElementOf(vpcPeeringsIds))
					g.Expect(route.Spec.ForProvider.DestinationCIDRBlock).To(BeElementOf(vpcPeeringsCIDRBlock))
				} else {
					g.Expect(route.Spec.ForProvider.VPCPeeringConnectionID).To(BeEquivalentTo(aws.String(vpcPeeringConnection.Annotations["crossplane.io/external-name"])))
					g.Expect(route.Spec.ForProvider.DestinationCIDRBlock).To(Or(BeEquivalentTo(vpcPeeringConnection.Status.AtProvider.RequesterVPCInfo.CIDRBlock), BeEquivalentTo(vpcPeeringConnection.Status.AtProvider.AccepterVPCInfo.CIDRBlock)))
				}
				g.Expect(route.Spec.ForProvider.Region).To(BeEquivalentTo(tc.clSpec.Region))
			}

			if tc.shouldCheckClustermeshStatus {
				var objectRef []*corev1.ObjectReference

				for _, route := range routes.Items {
					objectRef = append(objectRef, &corev1.ObjectReference{
						Kind:       route.Kind,
						APIVersion: route.APIVersion,
						Name:       route.Name,
					})
				}
				g.Expect(objectRef).To(ContainElements(clustermesh.Status.RoutesRef))
			}
		})
	}
}

func TestReconcileSecurityGroups(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	var testCases = []struct {
		description            string
		k8sObjects             []client.Object
		clustermesh            *clustermeshv1alpha1.ClusterMesh
		expectedSecurityGroups []securitygroupv1alpha2.SecurityGroup
	}{
		{
			description: "should create a securitygroup",
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name:      "A",
							Namespace: "kubernetes-A",
							CIDR:      "10.0.4.0/22",
						},
					},
				},
			},
			expectedSecurityGroups: []securitygroupv1alpha2.SecurityGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
				},
			},
		},
		{
			description: "should create securitygroup for clusters A and B",
			k8sObjects: []client.Object{
				&crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name:      "A",
							Namespace: "kubernetes-A",
							CIDR:      "10.0.4.0/22",
						},
						{
							Name:      "B",
							Namespace: "kubernetes-B",
							CIDR:      "10.0.8.0/22",
						},
					},
				},
			},
			expectedSecurityGroups: []securitygroupv1alpha2.SecurityGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "clustermesh-B-sg",
						Namespace: "kubernetes-B",
					},
				},
			},
		},
		{
			description: "should not create a securitygroup when it already exists",
			k8sObjects: []client.Object{
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name: "test-clustermesh",
							},
						},
					},
				},
			},
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name:      "A",
							Namespace: "kubernetes-A",
							CIDR:      "10.0.4.0/22",
						},
						{
							Name:      "B",
							Namespace: "kubernetes-B",
							CIDR:      "10.0.8.0/22",
						},
						{
							Name:      "C",
							Namespace: "kubernetes-C",
							CIDR:      "10.0.12.0/22",
						},
					},
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplaneSecurityGroupRef: []*corev1.ObjectReference{
						{
							Name:       "A",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "SecurityGroup",
						},
					},
				},
			},
			expectedSecurityGroups: []securitygroupv1alpha2.SecurityGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-A-sg",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-B-sg",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clustermesh-C-sg",
					},
				},
			},
		},
	}
	err := crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
				ReconcileSecurityGroupsFactory: func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error {
					return nil
				},
				Scheme: scheme.Scheme,
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				log:                   ctrl.LoggerFrom(ctx),
				clustermesh:           tc.clustermesh,
			}

			for _, cluster := range tc.clustermesh.Spec.Clusters {
				err = ReconcileSecurityGroups(reconciliation, ctx, cluster)
				g.Expect(err).ToNot(HaveOccurred())
			}
			for _, esg := range tc.expectedSecurityGroups {
				sg := &securitygroupv1alpha2.SecurityGroup{}
				key := client.ObjectKey{
					Name: esg.Name,
				}
				err = fakeClient.Get(ctx, key, sg)
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestClusterToClustersMapFunc(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	clusterA := &clusterv1beta1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "cluster.x-k8s.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"clustermesh.infrastructure.wildlife.io": "true",
			},
			Name:      "A",
			Namespace: "A",
			Labels: map[string]string{
				"clusterGroup": "test",
			},
		},
	}

	clustermesh := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterMesh",
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: clustermeshv1alpha1.ClusterMeshSpec{
			Clusters: []*clustermeshv1alpha1.ClusterSpec{
				{
					Name:      "A",
					Namespace: "A",
				},
				{
					Name:      "B",
					Namespace: "B",
				},
			},
		},
	}

	testCases := []struct {
		description string
		k8sObjects  []client.Object
		meshCreated bool
		wantPanic   bool
	}{
		{
			description: "if cluster A belongs to a mesh sould return cluster A and cluster B",
			k8sObjects: []client.Object{
				clusterA,
				clustermesh,
			},
			meshCreated: true,
		},

		{
			description: "if cluster A does not belongs to any mesh sould only return cluster A",
			k8sObjects: []client.Object{
				clusterA,
			},
		},

		{
			description: "should panic if object is not a cluster",
			wantPanic:   true,
		},
	}

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
			}

			defer func() {
				r := recover()
				if tc.wantPanic {
					g.Expect(r).To(ContainSubstring("Expected a Cluster but got a"))
				}
			}()

			var results []reconcile.Request
			if tc.wantPanic {
				results = reconciler.clusterToClustersMapFunc(context.TODO(), clustermesh)
			} else {
				results = reconciler.clusterToClustersMapFunc(context.TODO(), clusterA)
			}

			if tc.meshCreated {
				clusterNames := []string{}
				for _, clusterSpec := range clustermesh.Spec.Clusters {
					clusterNames = append(clusterNames, clusterSpec.Name)
				}

				g.Expect(len(results)).To(BeEquivalentTo(len(clusterNames)))

				for _, result := range results {
					g.Expect(result.Name).To(BeElementOf(clusterNames))
				}
			} else {
				g.Expect(results[0].Name).To(BeEquivalentTo(clusterA.Name))
			}
		})
	}
}

func TestValidateClusterMesh(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	clustermeshBase := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterMesh",
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
		},
		Spec: clustermeshv1alpha1.ClusterMeshSpec{
			Clusters: []*clustermeshv1alpha1.ClusterSpec{
				{
					Name:      "A",
					Namespace: "A",
				},
				{
					Name:      "B",
					Namespace: "B",
				},
			},
		},
	}

	vpcPeeringConnection := &crossec2v1alpha1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vpcPeeringConnectionAPIVersion,
			Kind:       "VPCPeeringConnection",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "vpc-a-b",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
					Kind:       "ClusterMesh",
					Name:       "clustermesh-test",
				},
			},
		},
		Status: crossec2v1alpha1.VPCPeeringConnectionStatus{
			ResourceStatus: v1.ResourceStatus{
				ConditionedStatus: *v1.NewConditionedStatus(v1.Available(), v1.ReconcileSuccess()),
			},
		},
	}

	vpcPeeringConnectionNotReady := vpcPeeringConnection.DeepCopy()
	vpcPeeringConnectionNotReady.Status = crossec2v1alpha1.VPCPeeringConnectionStatus{
		ResourceStatus: v1.ResourceStatus{
			ConditionedStatus: *v1.NewConditionedStatus(v1.Unavailable(), v1.ReconcileError(fmt.Errorf("reconcile error"))),
		},
	}

	route := &crossec2v1alpha1.Route{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
			Kind:       "Route",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "rt-xxxx-pcx-xxxx",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
					Name:       "vpc-a-b",
				},
			},
		},
		Status: crossec2v1alpha1.RouteStatus{
			ResourceStatus: v1.ResourceStatus{
				ConditionedStatus: *v1.NewConditionedStatus(v1.Available(), v1.ReconcileSuccess()),
			},
		},
	}

	route2Unavaiable := &crossec2v1alpha1.Route{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
			Kind:       "Route",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "rt-xxxx-pcx-zzzzz",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
					Name:       "vpc-a-b",
				},
			},
		},
		Status: crossec2v1alpha1.RouteStatus{
			ResourceStatus: v1.ResourceStatus{
				ConditionedStatus: *v1.NewConditionedStatus(v1.Unavailable(), v1.ReconcileSuccess()),
			},
		},
	}

	routeNotReady := route.DeepCopy()
	routeNotReady.Status = crossec2v1alpha1.RouteStatus{
		ResourceStatus: v1.ResourceStatus{
			ConditionedStatus: *v1.NewConditionedStatus(v1.Unavailable(), v1.ReconcileError(fmt.Errorf("reconcile error"))),
		},
	}

	securityGroup := &crossec2v1beta1.SecurityGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ec2.aws.crossplane.io/v1beta1",
			Kind:       "SecurityGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "sg-test",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
					Kind:       "ClusterMesh",
					Name:       "clustermesh-test",
				},
			},
		},
		Status: crossec2v1beta1.SecurityGroupStatus{
			ResourceStatus: v1.ResourceStatus{
				ConditionedStatus: *v1.NewConditionedStatus(v1.Available(), v1.ReconcileSuccess()),
			},
		},
	}

	securityGroupNotReady := securityGroup.DeepCopy()
	securityGroupNotReady.Status = crossec2v1beta1.SecurityGroupStatus{
		ResourceStatus: v1.ResourceStatus{
			ConditionedStatus: *v1.NewConditionedStatus(v1.Unavailable(), v1.ReconcileError(fmt.Errorf("reconcile error"))),
		},
	}

	testCases := []struct {
		description           string
		k8sObjects            []client.Object
		vpcNotReady           bool
		routeNotReady         bool
		securityGroupNotReady bool
		vpcAndRouteNotReady   bool
	}{
		{
			description: "should validate all objects sucessfull",
			k8sObjects: []client.Object{
				clustermeshBase,
				vpcPeeringConnection,
				route,
				securityGroup,
			},
		},
		{
			description: "should not validate vpcPeering, and should validate all other objects",
			k8sObjects: []client.Object{
				clustermeshBase,
				vpcPeeringConnectionNotReady,
				route,
				securityGroup,
			},
			vpcNotReady: true,
		},
		{
			description: "should not validate routes, and should validate all other objects",
			k8sObjects: []client.Object{
				clustermeshBase,
				vpcPeeringConnection,
				routeNotReady,
				securityGroup,
			},
			routeNotReady: true,
		},
		{
			description: "should not validate securityGroup, and should validate all other objects",
			k8sObjects: []client.Object{
				clustermeshBase,
				vpcPeeringConnection,
				route,
				securityGroupNotReady,
			},
			securityGroupNotReady: true,
		},
		{
			description: "should not validate route with one route working and the other unavailable",
			k8sObjects: []client.Object{
				clustermeshBase,
				vpcPeeringConnection,
				route,
				route2Unavaiable,
				securityGroup,
			},
			routeNotReady: true,
		},
	}

	err := crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			clustermesh := &clustermeshv1alpha1.ClusterMesh{}
			err := fakeClient.Get(ctx, client.ObjectKeyFromObject(clustermeshBase), clustermesh)
			g.Expect(err).To(BeNil())

			reconciler := ClusterMeshReconciler{
				Client: fakeClient,
			}

			reconciliation := &ClusterMeshReconciliation{
				ClusterMeshReconciler: reconciler,
				log:                   ctrl.LoggerFrom(ctx),
				clustermesh:           clustermesh,
			}

			err = reconciliation.validateClusterMesh(ctx)
			g.Expect(err).To(BeNil())

			if tc.vpcNotReady {
				g.Expect(conditions.IsFalse(clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition)).To(BeTrue())
			} else if tc.routeNotReady {
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsFalse(clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition)).To(BeTrue())
			} else if tc.securityGroupNotReady {
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsFalse(clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition)).To(BeTrue())
			} else if tc.vpcAndRouteNotReady {
				g.Expect(conditions.IsFalse(clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsFalse(clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition)).To(BeTrue())
			} else {
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition)).To(BeTrue())
				g.Expect(conditions.IsTrue(clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition)).To(BeTrue())
			}

		})
	}
}
