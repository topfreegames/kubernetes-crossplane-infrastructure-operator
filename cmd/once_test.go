package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/dryrun"
)

func TestTargets(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	realClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "kcp-a"}},
		},
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "kcp-b"}},
		},
		&kinfrastructurev1alpha1.KopsMachinePool{
			ObjectMeta: metav1.ObjectMeta{Name: "nodes", Namespace: "ns"},
			Spec:       kinfrastructurev1alpha1.KopsMachinePoolSpec{ClusterName: "kcp-b"},
		},
		&securitygroupv1alpha2.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "sg-both"},
			Spec: securitygroupv1alpha2.SecurityGroupSpec{
				InfrastructureRef: []*corev1.ObjectReference{
					{Kind: "KopsMachinePool", Namespace: "ns", Name: "nodes"},
					{Kind: "KopsMachinePool", Namespace: "ns", Name: "deleted"},
					{Kind: "KopsControlPlane", Namespace: "ns", Name: "kcp-a"},
				},
			},
		},
		&securitygroupv1alpha2.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "sg-paused",
				Annotations: map[string]string{sgcontroller.AnnotationKeyReconciliationPaused: "true"},
			},
			Spec: securitygroupv1alpha2.SecurityGroupSpec{
				InfrastructureRef: []*corev1.ObjectReference{{Kind: "KopsControlPlane", Namespace: "ns", Name: "kcp-b"}},
			},
		},
	).Build()
	clusterMeshReconciler, sgReconciler := newReconcilers(realClient)

	clusters, err := clusterTargets(ctx, realClient, clusterMeshReconciler)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(clusters).To(HaveLen(2))
	g.Expect(clusters[0].Source).To(Equal(dryrun.Source{Kind: "Cluster", Namespace: "ns", Name: "cluster-a", Clusters: []string{"kcp-a"}}))

	sgs, err := securityGroupTargets(ctx, realClient, sgReconciler)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(sgs).To(HaveLen(2))
	g.Expect(sgs[0].Source).To(Equal(dryrun.Source{Kind: "SecurityGroup", Name: "sg-both", Clusters: []string{"kcp-a", "kcp-b"}}))
	g.Expect(sgs[0].SkipReason).To(BeEmpty())
	g.Expect(sgs[1].Source.Name).To(Equal("sg-paused"))
	g.Expect(sgs[1].SkipReason).To(Equal("paused"))

	configureClusterFilter(clusterMeshReconciler, sgReconciler, "kcp-a")
	clusters, err = clusterTargets(ctx, realClient, clusterMeshReconciler)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(clusters).To(HaveLen(1))
	g.Expect(clusters[0].Source.Name).To(Equal("cluster-a"))
}

func TestIsPending(t *testing.T) {
	g := NewWithT(t)
	g.Expect(isPending(sgcontroller.ErrSecurityGroupNotAvailable)).To(BeTrue())
	g.Expect(isPending(errors.New("boom"))).To(BeFalse())
}

func TestWritePlanExitCode(t *testing.T) {
	g := NewWithT(t)
	source := dryrun.Source{Kind: "SecurityGroup", Name: "sg"}
	change := dryrun.CloudChange{Source: source, Kind: "Instance", ID: "i-1", Call: "ModifyInstanceAttribute", After: map[string][]string{"securityGroups": {"sg-1"}}}

	plan := dryrun.NewPlan()
	plan.SetResult(source, dryrun.StatusOK, "")
	var out strings.Builder
	g.Expect(writePlan(plan, nil, &out, "")).To(Equal(exitNoChanges))
	g.Expect(out.String()).To(ContainSubstring("No changes."))

	plan.RecordCloudChange(change)
	path := filepath.Join(t.TempDir(), "plan.json")
	g.Expect(writePlan(plan, nil, &strings.Builder{}, path)).To(Equal(exitChanges))
	data, err := os.ReadFile(path)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(data)).To(And(ContainSubstring(`"cloud": 1`), ContainSubstring(`"id": "i-1"`)))

	g.Expect(writePlan(plan, errors.New("list failed"), &strings.Builder{}, "")).To(Equal(exitError))

	pending := dryrun.NewPlan()
	pending.SetResult(source, dryrun.StatusPending, "would run again in 30s")
	g.Expect(writePlan(pending, nil, &strings.Builder{}, "")).To(Equal(exitError))

	plan.SetResult(dryrun.Source{Kind: "SecurityGroup", Name: "failed"}, dryrun.StatusError, "boom")
	g.Expect(writePlan(plan, nil, &strings.Builder{}, "")).To(Equal(exitError))

	g.Expect(writePlan(plan, nil, &strings.Builder{}, filepath.Join(t.TempDir(), "missing", "plan.json"))).To(Equal(exitError))
}

func TestPlanOnceFailsWhenNoObjectMatchesCluster(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	realClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "kcp-a"}},
		},
	).Build()
	clusterMeshReconciler, sgReconciler := newReconcilers(realClient)
	configureClusterFilter(clusterMeshReconciler, sgReconciler, "typo")

	plan := dryrun.NewPlan()
	err := planOnce(ctx, realClient, plan, clusterMeshReconciler, sgReconciler, 1, 1, "typo")
	g.Expect(err).To(MatchError("no Cluster or SecurityGroup matches --cluster=typo"))
	g.Expect(writePlan(plan, err, &strings.Builder{}, "")).To(Equal(exitError))
}

func TestPausedSecurityGroupWithoutInfrastructureRefIsReconciled(t *testing.T) {
	g := NewWithT(t)
	realClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&securitygroupv1alpha2.SecurityGroup{ObjectMeta: metav1.ObjectMeta{
			Name:        "sg-paused",
			Annotations: map[string]string{sgcontroller.AnnotationKeyReconciliationPaused: "true"},
		}},
	).Build()
	_, sgReconciler := newReconcilers(realClient)

	sgs, err := securityGroupTargets(context.TODO(), realClient, sgReconciler)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(sgs).To(HaveLen(1))
	g.Expect(sgs[0].SkipReason).To(BeEmpty())
}
