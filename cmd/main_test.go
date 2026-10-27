package main

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	clustermeshcontrollers "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/clustermesh.infrastructure"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/dryrun"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"
	fakeocean "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot/fake"
)

func newReconcilers(c client.Client) (*clustermeshcontrollers.ClusterMeshReconciler, *sgcontroller.SecurityGroupReconciler) {
	newEC2Client := func(cfg aws.Config) ec2.EC2Client { return &fakeec2.MockEC2Client{} }
	return &clustermeshcontrollers.ClusterMeshReconciler{
		Client:              c,
		NewEC2ClientFactory: newEC2Client,
	}, &sgcontroller.SecurityGroupReconciler{
		Client:                          c,
		Recorder:                        record.NewFakeRecorder(10),
		NewEC2ClientFactory:             newEC2Client,
		NewAutoScalingClientFactory:     func(cfg aws.Config) autoscaling.AutoScalingClient { return &fakeasg.MockAutoScalingClient{} },
		NewOceanCloudProviderAWSFactory: func() spot.OceanClient { return &fakeocean.MockOceanCloudProviderAWS{} },
	}
}

func TestConfigureDryRun(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	realClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	clusterMeshReconciler, sgReconciler := newReconcilers(realClient)

	configureDryRun(clusterMeshReconciler, sgReconciler, realClient)

	for _, c := range []client.Client{clusterMeshReconciler.Client, sgReconciler.Client} {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "default"}}
		g.Expect(c.Create(ctx, cm)).To(Succeed())
		err := realClient.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}

	g.Expect(clusterMeshReconciler.NewEC2ClientFactory(aws.Config{})).To(BeAssignableToTypeOf(&dryrun.EC2Client{}))

	g.Expect(sgReconciler.Recorder).NotTo(BeAssignableToTypeOf(&record.FakeRecorder{}))
	g.Expect(sgReconciler.NewEC2ClientFactory(aws.Config{})).To(BeAssignableToTypeOf(&dryrun.EC2Client{}))
	g.Expect(sgReconciler.NewAutoScalingClientFactory(aws.Config{})).To(BeAssignableToTypeOf(&dryrun.AutoScalingClient{}))
	g.Expect(sgReconciler.NewOceanCloudProviderAWSFactory()).To(BeAssignableToTypeOf(&dryrun.OceanClient{}))

	g.Expect(clusterMeshReconciler.RequestFilter).To(BeNil())
	g.Expect(sgReconciler.RequestFilter).To(BeNil())
}

func TestConfigureClusterFilter(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	realClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "cluster-a"}},
		},
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "cluster-b"}},
		},
		&securitygroupv1alpha2.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "sg-a"},
			Spec: securitygroupv1alpha2.SecurityGroupSpec{
				InfrastructureRef: []*corev1.ObjectReference{{Kind: "KopsControlPlane", Namespace: "ns", Name: "cluster-a"}},
			},
		},
		&securitygroupv1alpha2.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "sg-b"},
			Spec: securitygroupv1alpha2.SecurityGroupSpec{
				InfrastructureRef: []*corev1.ObjectReference{{Kind: "KopsControlPlane", Namespace: "ns", Name: "cluster-b"}},
			},
		},
	).Build()
	clusterMeshReconciler, sgReconciler := newReconcilers(realClient)

	configureClusterFilter(clusterMeshReconciler, sgReconciler, "cluster-a")

	ok, err := clusterMeshReconciler.RequestFilter(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "cluster-a"}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeTrue())

	ok, err = clusterMeshReconciler.RequestFilter(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "cluster-b"}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeFalse())

	ok, err = sgReconciler.RequestFilter(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: "sg-a"}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeTrue())

	ok, err = sgReconciler.RequestFilter(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: "sg-b"}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeFalse())
}

func TestManagerOptions(t *testing.T) {
	testCases := []struct {
		description          string
		enableLeaderElection bool
		dryRun               bool
		expectedLeader       bool
		expectedMetricsAddr  string
	}{
		{"should keep the flags without dry-run", true, false, true, ":8080"},
		{"should disable leader election and metrics with dry-run", true, true, false, "0"},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			g := NewWithT(t)
			opts := managerOptions(":8080", ":8081", ":8082", tc.enableLeaderElection, tc.dryRun)
			g.Expect(opts.LeaderElection).To(Equal(tc.expectedLeader))
			g.Expect(opts.Metrics.BindAddress).To(Equal(tc.expectedMetricsAddr))
			g.Expect(opts.HealthProbeBindAddress).To(Equal(":8081"))
			g.Expect(opts.PprofBindAddress).To(Equal(":8082"))
			g.Expect(opts.LeaderElectionID).To(Equal("1334ff7b.infrastructure.wildlife.io"))
			g.Expect(opts.Controller.UsePriorityQueue).To(Equal(ptr.To(false)))
		})
	}
}
