package clustermesh

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	crossec2v1alpha1 "github.com/crossplane/provider-aws/apis/ec2/v1alpha1"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/provider-crossplane/pkg/aws/ec2/fake"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClusterMeshReconciler(t *testing.T) {
	testCases := []struct {
		description                    string
		k8sObjects                     []client.Object
		expectedClusterMesh            []clustermeshv1beta1.ClusterMesh
		expectedClusterMeshToBeDeleted []string
	}{
		{
			description: "should do nothing if cluster don't have the clustermesh annotation",
			k8sObjects: []client.Object{
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
				},
			},
		},
		{
			description: "should do nothing if cluster don't have the label with clusterGroup",
			k8sObjects: []client.Object{
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
				},
			},
		},
		{
			description: "should create clustermesh when cluster has correct annotations and is the only cluster in mesh",
			k8sObjects: []client.Object{
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
				},
			},
			expectedClusterMesh: []clustermeshv1beta1.ClusterMesh{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-mesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				},
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-mesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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

	err = clustermeshv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
				PopulateClusterSpecFactory: func(r *ClusterMeshReconciler, ctx context.Context, cluster *clusterv1beta1.Cluster, kcp *kcontrolplanev1alpha1.KopsControlPlane) (*clustermeshv1beta1.ClusterSpec, error) {
					return &clustermeshv1beta1.ClusterSpec{
						Name:   cluster.Name,
						VPCID:  "xxx",
						Region: "yyy",
					}, nil
				},
				ReconcilePeeringsFactory: ReconcilePeerings,
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-cluster",
				},
			})
			g.Expect(err).To(BeNil())
			for _, clustermesh := range tc.expectedClusterMesh {
				ccm := &clustermeshv1beta1.ClusterMesh{}
				key := client.ObjectKey{
					Name: clustermesh.Name,
				}
				err = fakeClient.Get(ctx, key, ccm)
				g.Expect(err).To(BeNil())
				g.Expect(ccm.Spec).To(BeEquivalentTo(clustermesh.Spec))
			}
			for _, clusterMeshName := range tc.expectedClusterMeshToBeDeleted {
				ccm := &clustermeshv1beta1.ClusterMesh{}
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

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description           string
		k8sObjects            []client.Object
		cluster               *clusterv1beta1.Cluster
		mockDescribeVPCOutput *mockDescribeVPCOutput
		errorValidation       func(error) bool
		expectedOutput        *clustermeshv1beta1.ClusterSpec
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
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: clusterv1beta1.ClusterSpec{
					ControlPlaneRef: &corev1.ObjectReference{
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
			expectedOutput: &clustermeshv1beta1.ClusterSpec{
				Name:   "cluster-test",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
		},
		{
			description: "should fail when not finding kcp",
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: clusterv1beta1.ClusterSpec{
					ControlPlaneRef: &corev1.ObjectReference{
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
			errorValidation: func(err error) bool {
				return apierrors.IsNotFound(err)
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
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: clusterv1beta1.ClusterSpec{
					ControlPlaneRef: &corev1.ObjectReference{
						Namespace: metav1.NamespaceDefault,
						Name:      "kcp-test",
					},
				},
			},
			mockDescribeVPCOutput: &mockDescribeVPCOutput{
				&awsec2.DescribeVpcsOutput{}, nil,
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
			}

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
			key := client.ObjectKey{
				Namespace: tc.cluster.Spec.ControlPlaneRef.Namespace,
				Name:      tc.cluster.Spec.ControlPlaneRef.Name,
			}
			err := fakeClient.Get(context.Background(), key, kcp)
			g.Expect(err).NotTo(HaveOccurred())

			clSpec, err := PopulateClusterSpec(reconciler, context.TODO(), tc.cluster, kcp)
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
		expectedOutput  bool
	}{
		{
			description:    "should return false when having no clustermeshes",
			expectedOutput: false,
		},
		{
			description: "should return true when the cluster is part of a clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
							{
								Name: "A",
							},
						},
					},
				},
			},
			expectedOutput: true,
		},
		{
			description: "should return false when the cluster isn't part of any clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh-A",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
							{
								Name: "B",
							},
							{
								Name: "C",
							},
						},
					},
				},
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh-B",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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

	err := clustermeshv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
			}

			belong, err := reconciler.isClusterBelongToAnyMesh("A")
			if tc.errorValidation != nil {
				g.Expect(tc.errorValidation(err)).To(BeTrue())
			} else {
				g.Expect(belong).To(BeEquivalentTo(tc.expectedOutput))
			}
		})
	}

}

func TestReconcileNormal(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description    string
		k8sObjects     []client.Object
		cluster        *clusterv1beta1.Cluster
		clustermesh    *clustermeshv1beta1.ClusterMesh
		expectedOutput *clustermeshv1beta1.ClusterMesh
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterMesh",
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-clustermesh",
					ResourceVersion: "1",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
			},
		},
		{
			description: "should add the cluster B in the spec of an already create clustermesh",
			k8sObjects: []client.Object{
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{},
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
			},
		},
	}

	err := clustermeshv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
				log:    ctrl.LoggerFrom(ctx),
				PopulateClusterSpecFactory: func(r *ClusterMeshReconciler, ctx context.Context, cluster *clusterv1beta1.Cluster, kcp *kcontrolplanev1alpha1.KopsControlPlane) (*clustermeshv1beta1.ClusterSpec, error) {
					return &clustermeshv1beta1.ClusterSpec{
						Name: cluster.Name,
					}, nil
				},
				ReconcilePeeringsFactory: func(r *ClusterMeshReconciler, ctx context.Context, clustermesh *clustermeshv1beta1.ClusterMesh) error {
					return nil
				},
			}

			_, _ = reconciler.reconcileNormal(ctx, tc.cluster, tc.clustermesh)
			clustermesh := &clustermeshv1beta1.ClusterMesh{}
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-clustermesh"}, clustermesh)
			g.Expect(clustermesh.GetName()).To(BeEquivalentTo(tc.expectedOutput.GetName()))
			g.Expect(clustermesh.Spec).To(BeEquivalentTo(tc.expectedOutput.Spec))
		})
	}
}

func TestReconcileDelete(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description    string
		k8sObjects     []client.Object
		cluster        *clusterv1beta1.Cluster
		clustermesh    *clustermeshv1beta1.ClusterMesh
		expectedOutput *clustermeshv1beta1.ClusterMesh
	}{
		{
			description: "should remove the cluster A from clustermesh spec",
			k8sObjects: []client.Object{
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
							{
								Name: "A",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
		},
		{
			description: "should not remove anything when the cluster don't exist in the clustermesh spec",
			k8sObjects: []client.Object{
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
							{
								Name: "B",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				&clustermeshv1beta1.ClusterMesh{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-clustermesh",
					},
					Spec: clustermeshv1beta1.ClusterMeshSpec{
						Clusters: []*clustermeshv1beta1.ClusterSpec{
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
			},
			cluster: &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "A",
					Labels: map[string]string{
						"clusterGroup": "test-clustermesh",
					},
				},
			},
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			expectedOutput: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
	}

	err := clustermeshv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
				log:    ctrl.LoggerFrom(ctx),
				ReconcilePeeringsFactory: func(r *ClusterMeshReconciler, ctx context.Context, clustermesh *clustermeshv1beta1.ClusterMesh) error {
					return nil
				},
			}

			_ = reconciler.reconcileDelete(ctx, tc.cluster, tc.clustermesh)
			clustermesh := &clustermeshv1beta1.ClusterMesh{}
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-clustermesh"}, clustermesh)
			if tc.expectedOutput != nil {
				g.Expect(clustermesh.GetName()).To(BeEquivalentTo(tc.expectedOutput.GetName()))
				g.Expect(clustermesh.Spec).To(BeEquivalentTo(tc.expectedOutput.Spec))
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

		})
	}
}

func TestReconcilePeerings(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description                          string
		k8sObjects                           []client.Object
		clustermesh                          *clustermeshv1beta1.ClusterMesh
		expectedVpcPeeringConnections        []string
		shouldBeDeletedVpcPeeringConnections []string
	}{
		{
			description: "should not create a vpcpeering with only one cluster",
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
			},
		},
		{
			description: "should create vpcpeeringconnection A-B, A-C, and B-C",
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
			expectedVpcPeeringConnections: []string{"A-B", "A-C", "B-C"},
		},
		{
			description: "should not create a vpcpeering when it already exists",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				Status: clustermeshv1beta1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"A-B", "A-C", "B-C"},
		},
		{
			description: "should not create a vpcpeering when it already exists inverted",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				Status: clustermeshv1beta1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "B-A",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"B-A", "A-C", "B-C"},
		},
		{
			description: "should create correctly with different spec orders",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
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
				Status: clustermeshv1beta1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections: []string{"A-B", "C-A", "C-B"},
		},
		{
			description: "should remove vpcpeerings related with cluster C",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
						{
							Name: "B",
						},
						{
							Name: "A",
						},
					},
				},
				Status: clustermeshv1beta1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "A-C",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "B-C",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			expectedVpcPeeringConnections:        []string{"A-B"},
			shouldBeDeletedVpcPeeringConnections: []string{"A-C", "B-C"},
		},
		{
			description: "should remove last vpcpeering of the clustermesh",
			k8sObjects: []client.Object{
				&crossec2v1alpha1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
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
			clustermesh: &clustermeshv1beta1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Spec: clustermeshv1beta1.ClusterMeshSpec{
					Clusters: []*clustermeshv1beta1.ClusterSpec{
						{
							Name: "A",
						},
					},
				},
				Status: clustermeshv1beta1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: "ec2.aws.crossplane.io/v1alpha1",
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			shouldBeDeletedVpcPeeringConnections: []string{"A-B"},
		},
	}

	err := crossec2v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {

			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
				log:    ctrl.LoggerFrom(ctx),
				ReconcilePeeringsFactory: func(r *ClusterMeshReconciler, ctx context.Context, clustermesh *clustermeshv1beta1.ClusterMesh) error {
					return nil
				},
			}

			_ = ReconcilePeerings(reconciler, ctx, tc.clustermesh)
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

		})
	}
}
