package clustermesh

import (
	clustermeshv1alpha1 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/apis/clustermesh/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Label      = "clusterGroup"
	Annotation = "clustermesh.infrastructure.wildlife.io"
)

func New(name string, clSpecs ...*clustermeshv1alpha1.ClusterSpec) *clustermeshv1alpha1.ClusterMesh {
	ccm := &clustermeshv1alpha1.ClusterMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: clustermeshv1alpha1.ClusterMeshSpec{
			Clusters: clSpecs,
		},
	}
	return ccm
}

func GetClusterMeshSecurityGroupName(clusterName string) string {
	return "clustermesh-" + clusterName + "-sg"
}
