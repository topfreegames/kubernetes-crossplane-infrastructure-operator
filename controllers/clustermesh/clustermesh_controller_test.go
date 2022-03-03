package clustermesh

import (
	"context"
	"testing"

	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClusterMeshReconciler(t *testing.T) {

	cluster1 := &clusterv1beta1.Cluster{
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

	cluster2 := &clusterv1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster2",
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

	clusterNoMesh := &clusterv1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster2",
		},
	}

	testCases := []map[string]interface{}{
		{
			"description": "should create a Crossplane ClusterMesh",
			"k8sObjects": []client.Object{
				cluster1,
			},
			"expectedError": false,
		},
		{
			"description": "should create Crossplane ClusterMesh and add second cluster to the list",
			"k8sObjects": []client.Object{
				cluster1, cluster2,
			},
			"expectedError": false,
			"shouldUpdate":  true,
		},
		{
			"description": "should pass if cluster object dont have the clustermesh annotation",
			"k8sObjects": []client.Object{
				clusterNoMesh,
			},
			"expectedError": false,
			"noMesh":        true,
		},
		{
			"description": "should pass after the second reconcile loop",
			"k8sObjects": []client.Object{
				cluster1,
			},
			"expectedError": false,
			"secondLoop":    true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()

			k8sObjects := tc["k8sObjects"].([]client.Object)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(k8sObjects...).Build()
			reconciler := &ClusterMeshReconciler{
				Client: fakeClient,
			}
			for _, cluster := range k8sObjects {
				_, err := reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: metav1.NamespaceDefault,
						Name:      cluster.GetName(),
					},
				})
				if _, ok := tc["secondLoop"].(bool); ok {
					_, err = reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: client.ObjectKey{
							Namespace: metav1.NamespaceDefault,
							Name:      cluster.GetName(),
						},
					})
				}
				if err != nil {
					break
				}
			}
			if !tc["expectedError"].(bool) {
				crosscm := &clustermeshv1beta1.ClusterMesh{}
				key := client.ObjectKey{
					Name: "testmesh",
				}
				err = fakeClient.Get(ctx, key, crosscm)
				if _, ok := tc["noMesh"].(bool); ok {
					g.Expect(err).ToNot(BeNil())
				} else {
					g.Expect(err).To(BeNil())
				}

				g.Expect(crosscm).NotTo(BeNil())

				if _, ok := tc["shouldUpdate"].(bool); ok {
					g.Expect(crosscm.Spec.ClusterRefList).To(ContainElement(ContainSubstring("test-cluster2")))
				}

			} else {
				g.Expect(err).ToNot(BeNil())
			}

		})
	}
}
