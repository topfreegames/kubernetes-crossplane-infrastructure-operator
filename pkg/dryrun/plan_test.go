package dryrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	clustermeshcontrollers "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/clustermesh.infrastructure"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
)

func init() {
	// Show the whole plan when a comparison fails.
	format.MaxLength = 0
	format.TruncatedDiff = false
}

var source = Source{Kind: "Cluster", Namespace: "ns", Name: "a", Clusters: []string{"a"}}

func writePlan(g *WithT, p *Plan) (string, Summary) {
	var b strings.Builder
	s, err := p.Write(&b)
	g.Expect(err).NotTo(HaveOccurred())
	return b.String(), s
}

func TestPlanRecordsKubernetesWrites(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	ctx := WithPlan(context.TODO(), plan, source)

	unchanged := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unchanged", Namespace: "default"}, Data: map[string]string{"key": "value"}}
	updated := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "updated", Namespace: "default"}, Data: map[string]string{"key": "old"}}
	patched := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "patched", Namespace: "default"}}
	deleted := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "deleted", Namespace: "default"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"}, Status: corev1.PodStatus{Phase: corev1.PodPending}}
	real := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(unchanged, updated, patched, deleted, pod).WithStatusSubresource(pod).Build()
	c := NewClient(real, real)

	get := func(obj client.Object) {
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
	}
	get(unchanged)
	g.Expect(c.Update(ctx, unchanged)).To(Succeed())

	get(updated)
	updated.Data["key"] = "new"
	g.Expect(c.Update(ctx, updated)).To(Succeed())

	get(patched)
	base := patched.DeepCopy()
	patched.Finalizers = []string{"example.com/finalizer"}
	g.Expect(c.Patch(ctx, patched, client.MergeFrom(base))).To(Succeed())

	g.Expect(c.Delete(ctx, deleted)).To(Succeed())
	g.Expect(c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "created", Namespace: "default"}, Data: map[string]string{"key": "value"}})).To(Succeed())

	// A status update and an update of the main resource are one change.
	get(pod)
	pod.Labels = map[string]string{"label": "added"}
	g.Expect(c.Update(ctx, pod)).To(Succeed())
	pod.Status.Phase = corev1.PodRunning
	g.Expect(c.Status().Update(ctx, pod)).To(Succeed())

	text, summary := writePlan(g, plan)
	g.Expect(summary).To(Equal(Summary{Create: 1, Update: 3, Delete: 1}))
	g.Expect(summary.ExitCode()).To(Equal(ExitChanges))
	g.Expect(text).To(Equal(`# cluster a
+ ConfigMap default/created (create)
+ data:
+   key: value
+ metadata:
+   name: created
+   namespace: default
- ConfigMap default/deleted (delete)
~ ConfigMap default/patched (update)
@@ -1,3 +1,5 @@
 metadata:
+  finalizers:
+  - example.com/finalizer
   name: patched
   namespace: default
~ ConfigMap default/updated (update)
@@ -1,4 +1,4 @@
 data:
-  key: old
+  key: new
 metadata:
   name: updated
~ Pod default/pod (update)
@@ -1,3 +1,5 @@
 metadata:
+  labels:
+    label: added
   name: pod
   namespace: default
@@ -5,3 +7,3 @@
   containers: null
 status:
-  phase: Pending
+  phase: Running

Plan: 1 to create, 3 to update (0 only reorder lists), 1 to delete, 0 AWS/Spot changes. Results: 0 ok, 0 skipped (paused), 0 pending, 0 errors.
`))
}

func TestPlanLabels(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	key := func(name string) objectKey { return objectKey{Group: "example.com", Kind: "Thing", Name: name} }
	ref := func(name string) interface{} { return map[string]interface{}{"name": name} }
	rule := func(cidrs ...interface{}) interface{} { return map[string]interface{}{"cidrs": cidrs} }
	meta := func(finalizers ...interface{}) map[string]interface{} {
		m := map[string]interface{}{"deletionTimestamp": "2025-05-13T15:28:24Z"}
		if len(finalizers) > 0 {
			m["finalizers"] = finalizers
		}
		return map[string]interface{}{"metadata": m}
	}

	plan.recordStatusUpdate(source, key("reordered"),
		map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("b"), ref("a")}, "rules": []interface{}{rule("2", "1")}}},
		map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("a"), ref("b")}, "rules": []interface{}{rule("1", "2")}}})
	// An empty list and a missing list are the same.
	plan.recordUpdate(source, key("empty-list"), map[string]interface{}{"spec": map[string]interface{}{}}, map[string]interface{}{"spec": map[string]interface{}{"refs": []interface{}{}}})
	plan.recordUpdate(source, key("released"), meta("example.com/sg"), meta())
	plan.recordUpdate(source, key("kept"), meta("example.com/sg", "example.com/other"), meta("example.com/other"))
	plan.recordDelete(source, key("already-deleting"), meta("example.com/sg"), nil)

	text, summary := writePlan(g, plan)
	g.Expect(summary).To(Equal(Summary{Update: 2, Reorder: 1, Delete: 1}))
	g.Expect(text).To(Equal(`# cluster a
~ Thing.example.com kept (update)
@@ -3,3 +3,2 @@
   finalizers:
   - example.com/other
-  - example.com/sg
- Thing.example.com released (delete, last finalizer removed)
@@ -1,4 +1,2 @@
 metadata:
   deletionTimestamp: "2025-05-13T15:28:24Z"
-  finalizers:
-  - example.com/sg
~ Thing.example.com reordered (update, order only)

Plan: 0 to create, 2 to update (1 only reorder lists), 1 to delete, 0 AWS/Spot changes. Results: 0 ok, 0 skipped (paused), 0 pending, 0 errors.
`))
}

func TestPlanShowsEachVersionOfAnObject(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	key := objectKey{Group: "example.com", Kind: "Mesh", Name: "mesh"}
	live := map[string]interface{}{"status": map[string]interface{}{"ready": false}}
	for _, name := range []string{"a", "b"} {
		plan.recordStatusUpdate(Source{Kind: "Cluster", Name: name, Clusters: []string{name}}, key, live, map[string]interface{}{"status": map[string]interface{}{"ready": true}})
	}
	plan.recordStatusUpdate(Source{Kind: "Cluster", Name: "c", Clusters: []string{"c"}}, key, live, map[string]interface{}{"status": map[string]interface{}{"ready": false, "reason": "x"}})

	text, summary := writePlan(g, plan)
	g.Expect(summary.Update).To(Equal(1))
	g.Expect(text).To(HavePrefix(`# cluster a, b, c
~ Mesh.example.com mesh (update)
  # update from Cluster/a, Cluster/b
@@ -1,2 +1,2 @@
 status:
-  ready: false
+  ready: true
  # update from Cluster/c
@@ -1,2 +1,3 @@
 status:
   ready: false
+  reason: x
`))
}

func TestPlanHidesSecretValues(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	ctx := WithPlan(context.TODO(), plan, source)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "default"}, Data: map[string][]byte{"token": []byte("old-secret-value")}}
	real := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(secret).Build()
	c := NewClient(real, real)

	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
	secret.Data["token"] = []byte("new-secret-value")
	g.Expect(c.Update(ctx, secret)).To(Succeed())

	text, _ := writePlan(g, plan)
	g.Expect(text).To(ContainSubstring("token: (sensitive, sha256 "))
	g.Expect(text).NotTo(Or(ContainSubstring("secret-value"), ContainSubstring("c2VjcmV0")))
}

func TestPlanUpdateFailsWhenCurrentObjectCannotBeRead(t *testing.T) {
	g := NewWithT(t)
	real := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	c := NewClient(real, real)
	missing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}

	err := c.Update(WithPlan(context.TODO(), NewPlan(), source), missing)
	g.Expect(err).To(MatchError(ContainSubstring("dry-run plan")))
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

	// Without a plan, the update goes on and only the log misses the patch.
	err = c.Update(context.TODO(), missing)
	if err != nil {
		g.Expect(err.Error()).NotTo(ContainSubstring("dry-run plan"))
	}
}

func TestPlanCloudChanges(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	lines := []string{"~ Instance i-1 (ModifyInstanceAttribute)", "-   security groups [sg-1]", "+   security groups [sg-1, sg-2]"}
	plan.RecordCloudChange(Source{Kind: "SecurityGroup", Name: "x", Clusters: []string{"a"}}, lines...)
	plan.RecordCloudChange(Source{Kind: "SecurityGroup", Name: "x", Clusters: []string{"a"}}, lines...)
	plan.RecordCloudChange(Source{Kind: "SecurityGroup", Name: "y", Clusters: []string{"a"}}, lines...)
	g.Expect(plan.cloud[strings.Join(lines, "\n")].sources).To(HaveLen(2))

	text, summary := writePlan(g, plan)
	g.Expect(summary.Cloud).To(Equal(1))
	g.Expect(text).To(HavePrefix("# cluster a\n" + strings.Join(lines, "\n") + "\n\n"))

	plan.forget(Source{Kind: "SecurityGroup", Name: "x"})
	plan.forget(Source{Kind: "SecurityGroup", Name: "y"})
	_, summary = writePlan(g, plan)
	g.Expect(summary.ExitCode()).To(Equal(ExitNoChanges))
}

func TestPlanRun(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	plan.recordCreate(Source{Kind: "Cluster", Name: "creator"}, objectKey{Group: "ec2.aws.crossplane.io", Kind: "SecurityGroup", Name: "sg"}, nil, map[string]interface{}{})
	real := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	c := NewClient(real, real)
	conflicts := 0

	reconcile := func(result ctrl.Result, err error) func(context.Context) (ctrl.Result, error) {
		return func(context.Context) (ctrl.Result, error) { return result, err }
	}
	notFound := func(group string) error {
		return apierrors.NewNotFound(schema.GroupResource{Group: group, Resource: "securitygroups"}, "sg")
	}
	targets := []target{
		{source: Source{Name: "ok"}, reconcile: reconcile(ctrl.Result{RequeueAfter: time.Hour}, nil)},
		{source: Source{Name: "paused"}, skipReason: "paused", reconcile: func(context.Context) (ctrl.Result, error) {
			panic("a skipped target was reconciled")
		}},
		{source: Source{Name: "requeue"}, reconcile: reconcile(ctrl.Result{RequeueAfter: 30 * time.Second}, nil)},
		{source: Source{Name: "sg-not-available"}, reconcile: reconcile(ctrl.Result{}, sgcontroller.ErrSecurityGroupNotAvailable)},
		{source: Source{Name: "needs-created"}, reconcile: reconcile(ctrl.Result{}, notFound("ec2.aws.crossplane.io"))},
		{source: Source{Name: "needs-other-group"}, reconcile: reconcile(ctrl.Result{}, notFound("ec2.aws.wildlife.io"))},
		{source: Source{Name: "failed"}, reconcile: reconcile(ctrl.Result{}, errors.New("boom"))},
		{source: Source{Name: "panic"}, reconcile: func(context.Context) (ctrl.Result, error) { panic("oops") }},
		{source: Source{Name: "ignored-write-error"}, reconcile: func(ctx context.Context) (ctrl.Result, error) {
			missing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
			_ = c.Patch(ctx, missing, client.MergeFrom(missing.DeepCopy()))
			// Expected write errors do not fail the reconcile.
			recordWriteError(ctx, "create", missing, apierrors.NewAlreadyExists(schema.GroupResource{}, "missing"))
			return ctrl.Result{}, nil
		}},
		{source: Source{Name: "conflict"}, reconcile: func(ctx context.Context) (ctrl.Result, error) {
			conflicts++
			if conflicts == 1 {
				recordWriteError(ctx, "patch", &corev1.ConfigMap{}, apierrors.NewConflict(schema.GroupResource{}, "cm", errors.New("changed")))
			}
			return ctrl.Result{}, nil
		}},
	}
	plan.run(context.TODO(), 3, targets)

	g.Expect(conflicts).To(Equal(2))
	statuses := map[string]Status{}
	for _, r := range plan.results {
		statuses[r.source.Name] = r.status
	}
	g.Expect(statuses).To(Equal(map[string]Status{
		"ok":                  StatusOK,
		"paused":              StatusSkipped,
		"requeue":             StatusPending,
		"sg-not-available":    StatusPending,
		"needs-created":       StatusPending,
		"needs-other-group":   StatusError,
		"failed":              StatusError,
		"panic":               StatusError,
		"ignored-write-error": StatusError,
		"conflict":            StatusOK,
	}))
	g.Expect(plan.results["/ignored-write-error"].reason).To(ContainSubstring("a write failed: patch default/missing"))

	text, summary := writePlan(g, plan)
	g.Expect(summary.ExitCode()).To(Equal(ExitError))
	g.Expect(text).To(ContainSubstring("! error /failed: boom\n"))
}

func TestPlanRunStopsAtDeadline(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	ctx, cancel := context.WithCancel(context.TODO())
	plan.run(ctx, 1, []target{
		{source: Source{Name: "slow"}, reconcile: func(ctx context.Context) (ctrl.Result, error) {
			cancel()
			<-ctx.Done()
			return ctrl.Result{}, ctx.Err()
		}},
		{source: Source{Name: "late"}, reconcile: func(context.Context) (ctrl.Result, error) { return ctrl.Result{}, nil }},
	})

	g.Expect(plan.results["/late"].reason).To(Equal("not reconciled: context canceled"))
	_, summary := writePlan(g, plan)
	g.Expect(summary.Errors).To(Equal(2))
}

func TestPlanOnce(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	g.Expect(clusterv1beta1.AddToScheme(scheme)).To(Succeed())
	g.Expect(kinfrastructurev1alpha1.AddToScheme(scheme)).To(Succeed())
	g.Expect(securitygroupv1alpha2.AddToScheme(scheme)).To(Succeed())

	paused := map[string]string{sgcontroller.AnnotationKeyReconciliationPaused: "true"}
	real := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		// The reconciler fails on a Cluster without a KopsControlPlane.
		&clusterv1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "ns"}},
		&securitygroupv1alpha2.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "sg-paused", Annotations: paused},
			Spec:       securitygroupv1alpha2.SecurityGroupSpec{InfrastructureRef: []*corev1.ObjectReference{{Kind: "KopsControlPlane", Name: "kcp-a"}}},
		},
		// The reconciler fails on a SecurityGroup without infrastructureRef
		// before it checks the pause annotation.
		&securitygroupv1alpha2.SecurityGroup{ObjectMeta: metav1.ObjectMeta{Name: "sg-no-ref", Annotations: paused}},
	).Build()
	cm := &clustermeshcontrollers.ClusterMeshReconciler{Client: real}
	sg := &sgcontroller.SecurityGroupReconciler{Client: real}

	plan := NewPlan()
	g.Expect(plan.Once(context.TODO(), real, cm, sg, 1, 1, "")).To(Succeed())
	g.Expect(plan.results["Cluster/ns/cluster-a"].status).To(Equal(StatusError))
	g.Expect(plan.results["SecurityGroup/sg-paused"].status).To(Equal(StatusSkipped))
	g.Expect(plan.results["SecurityGroup/sg-no-ref"].status).To(Equal(StatusError))

	// A --cluster that matches nothing fails instead of planning no changes.
	cm.RequestFilter = func(context.Context, ctrl.Request) (bool, error) { return false, nil }
	sg.RequestFilter = cm.RequestFilter
	err := NewPlan().Once(context.TODO(), real, cm, sg, 1, 1, "typo")
	g.Expect(err).To(MatchError("no Cluster matches --cluster=typo"))
}

func TestSecurityGroupClusters(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(kinfrastructurev1alpha1.AddToScheme(scheme)).To(Succeed())
	real := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{Name: "nodes", Namespace: "ns"},
		Spec:       kinfrastructurev1alpha1.KopsMachinePoolSpec{ClusterName: "kcp-b"},
	}).Build()

	clusters, err := securityGroupClusters(context.TODO(), real, &securitygroupv1alpha2.SecurityGroup{
		Spec: securitygroupv1alpha2.SecurityGroupSpec{InfrastructureRef: []*corev1.ObjectReference{
			{Kind: "KopsMachinePool", Namespace: "ns", Name: "nodes"},
			{Kind: "KopsMachinePool", Namespace: "ns", Name: "deleted"},
			{Kind: "KopsControlPlane", Namespace: "ns", Name: "kcp-a"},
		}},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(clusters).To(Equal([]string{"kcp-a", "kcp-b"}))
}

func TestPlanLabelsAnObjectByAllItsChanges(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	key := objectKey{Group: "example.com", Kind: "Mesh", Name: "mesh"}
	ref := func(name string) interface{} { return map[string]interface{}{"name": name} }
	live := map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("b"), ref("a")}}}
	// Cluster a only sorts the refs, cluster b also adds one.
	plan.recordStatusUpdate(Source{Kind: "Cluster", Name: "a"}, key, live, map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("a"), ref("b")}}})
	plan.recordStatusUpdate(Source{Kind: "Cluster", Name: "b"}, key, live, map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("a"), ref("b"), ref("c")}}})

	text, summary := writePlan(g, plan)
	g.Expect(summary).To(Equal(Summary{Update: 1}))
	g.Expect(text).To(ContainSubstring("~ Mesh.example.com mesh (update)\n  # update, order only from Cluster/a\n  # update from Cluster/b\n"))
}

func TestPlanReleaseAndDeleteOfADeletingObject(t *testing.T) {
	g := NewWithT(t)
	live := map[string]interface{}{"metadata": map[string]interface{}{"deletionTimestamp": "2025-05-13T15:28:24Z", "finalizers": []interface{}{"example.com/sg"}}}
	released := map[string]interface{}{"metadata": map[string]interface{}{"deletionTimestamp": "2025-05-13T15:28:24Z"}}
	key := objectKey{Kind: "Pool", Name: "pool"}

	for _, deleteFirst := range []bool{true, false} {
		plan := NewPlan()
		if deleteFirst {
			plan.recordDelete(source, key, live, nil)
		}
		plan.recordUpdate(source, key, live, released)
		if !deleteFirst {
			plan.recordDelete(source, key, live, nil)
		}
		_, summary := writePlan(g, plan)
		g.Expect(summary).To(Equal(Summary{Delete: 1}), "delete first: %v", deleteFirst)
	}
}

func TestFailedPlanDoesNotSayNoChanges(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	plan.SetResult(Source{Kind: "Plan", Name: "run"}, StatusError, "listing Clusters: connection refused")

	text, summary := writePlan(g, plan)
	g.Expect(summary.ExitCode()).To(Equal(ExitError))
	g.Expect(text).NotTo(ContainSubstring("No changes."))
	g.Expect(text).To(ContainSubstring("! error Plan/run: listing Clusters: connection refused\n"))
}
