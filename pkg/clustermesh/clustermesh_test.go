package clustermesh_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clustermeshv1alpha1 "github.com/topfreegames/provider-crossplane/api/clustermesh.infrastructure/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/clustermesh"
)

func TestClustermesh(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Clustermesh Suite")
}

var _ = Describe("Clustermesh", func() {
	Context("Object creation", func() {
		type args struct {
			clMeshName string
			clusters   []*clustermeshv1alpha1.ClusterSpec
		}

		tests := []struct {
			description string
			args        args
			want        *clustermeshv1alpha1.ClusterMeshSpec
		}{
			{
				description: "should create a clustermesh with two clusters",
				args: args{
					clMeshName: "clustermesh-test",
					clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name:      "cl1",
							Namespace: "kubernetes-cl1",
						},
						{
							Name:      "cl2",
							Namespace: "kubernetes-cl2",
						},
					},
				},
				want: &clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						{
							Name:      "cl1",
							Namespace: "kubernetes-cl1",
						},
						{
							Name:      "cl2",
							Namespace: "kubernetes-cl2",
						},
					},
				},
			},
			{
				description: "should create a clustermesh without clusters",
				args: args{
					clMeshName: "clustermesh-test",
				},
				want: &clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{},
				},
			},
		}

		for _, tt := range tests {
			It(tt.description, func() {
				result := clustermesh.New(tt.args.clMeshName)
				Expect(result).ToNot(BeNil())
				Expect(len(tt.want.Clusters)).To(Equal(len(result.Spec.Clusters)))
				Expect(tt.args.clMeshName).To(Equal(result.ObjectMeta.Name))
			})
		}
	})

	Describe("Security Group naming for clusters inside a clustermesh", func() {

		type args struct {
			clusterName string
		}
		tests := []struct {
			description string
			args        args
			want        string
		}{
			{
				description: "must return a valid clustermesh security group name",
				args: args{
					clusterName: "test1",
				},
				want: "clustermesh-test1-sg",
			},
			{
				description: "must return a name only with the default pre/post-fix",
				args: args{
					clusterName: "",
				},
				want: "clustermesh--sg",
			},
		}
		for _, tt := range tests {
			It(tt.description, func() {
				Expect(clustermesh.GetClusterMeshSecurityGroupName(tt.args.clusterName)).To(Equal(tt.want))
			})
		}
	})
})
