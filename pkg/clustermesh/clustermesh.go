package clustermesh

import (
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Label      = "clusterGroup"
	Annotation = "clustermesh.infrastructure.wildlife.io"
)

// TODO: Coverage this constructor with tests
func NewClusterMesh(name string, clSpec *clustermeshv1beta1.ClusterSpec) *clustermeshv1beta1.ClusterMesh {
	clusters := []*clustermeshv1beta1.ClusterSpec{clSpec}
	ccm := &clustermeshv1beta1.ClusterMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: clustermeshv1beta1.ClusterMeshSpec{
			Clusters: clusters,
		},
	}
	return ccm
}

func GetClusterMeshSecurityGroupName(clusterName string) string {
	return "clustermesh-" + clusterName + "-sg"
}
