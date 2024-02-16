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
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	clustermeshv1alpha1 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/clustermesh.infrastructure/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "ClusterMesh Controller")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

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
				result := New(tt.args.clMeshName)
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
				Expect(getClusterMeshSecurityGroupName(tt.args.clusterName)).To(Equal(tt.want))
			})
		}
	})
})
