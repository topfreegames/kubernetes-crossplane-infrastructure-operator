package dryrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var source = Source{Kind: "Cluster", Namespace: "kubernetes-a", Name: "a", Clusters: []string{"a"}}

func reportText(g *WithT, r *Report) string {
	var b strings.Builder
	g.Expect(r.WriteText(&b)).To(Succeed())
	return b.String()
}

func TestPlanRecordsKubernetesWrites(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	plan := NewPlan()
	ctx := WithPlan(newLogContext(&lines), plan, source)

	unchanged := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "unchanged", Namespace: "default"},
		Data:       map[string]string{"key": "value"},
	}
	updated := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "updated", Namespace: "default"},
		Data:       map[string]string{"key": "old", "removed": "value"},
	}
	patched := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "patched", Namespace: "default"}}
	deleted := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "deleted", Namespace: "default"}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(unchanged, updated, patched, deleted, pod).WithStatusSubresource(pod).Build()
	c := NewClient(real, real)

	current := &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(unchanged), current)).To(Succeed())
	g.Expect(c.Update(ctx, current)).To(Succeed())

	current = &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(updated), current)).To(Succeed())
	current.Data = map[string]string{"key": "new"}
	g.Expect(c.Update(ctx, current)).To(Succeed())

	current = &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(patched), current)).To(Succeed())
	base := current.DeepCopy()
	current.Finalizers = []string{"example.com/finalizer"}
	g.Expect(c.Patch(ctx, current, client.MergeFrom(base))).To(Succeed())

	g.Expect(c.Delete(ctx, deleted.DeepCopy())).To(Succeed())

	created := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "created", Namespace: "default"},
		Data:       map[string]string{"key": "value"},
	}
	g.Expect(c.Create(ctx, created)).To(Succeed())

	currentPod := &corev1.Pod{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(pod), currentPod)).To(Succeed())
	currentPod.Labels = map[string]string{"label": "added"}
	g.Expect(c.Update(ctx, currentPod)).To(Succeed())
	currentPod.Status.Phase = corev1.PodRunning
	g.Expect(c.Status().Update(ctx, currentPod)).To(Succeed())

	report := plan.Report()
	g.Expect(report.Summary).To(Equal(Summary{Create: 1, Update: 3, Delete: 1}))
	g.Expect(report.HasChanges()).To(BeTrue())

	text := reportText(g, report)
	g.Expect(text).To(Equal(`# cluster a
+ ConfigMap default/created (create)
+     data.key: "value"
+     metadata.name: "created"
+     metadata.namespace: "default"
- ConfigMap default/deleted (delete)
~ ConfigMap default/patched (update)
+     metadata.finalizers[]: "example.com/finalizer"
~ ConfigMap default/updated (update)
-     data.key: "old"
+     data.key: "new"
-     data.removed: "value"
~ Pod default/pod (update)
+     metadata.labels.label: "added"
-     status.phase: "Pending"
+     status.phase: "Running"

Plan: 1 to create, 3 to update (0 only reorder lists), 1 to delete, 0 AWS/Spot changes. Reconciled 0 objects: 0 skipped (paused), 0 pending, 0 errors.
`))
	g.Expect(text).NotTo(ContainSubstring("unchanged"))
}

func TestPlanWithoutContextOnlyLogs(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	ctx := newLogContext(&lines)

	existing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "default"}}
	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(existing).Build()
	c := NewClient(real, real)

	// Without a plan, a failed read of the current object only drops the
	// patch from the log.
	missing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
	err := c.Update(ctx, missing)
	if err != nil {
		g.Expect(err.Error()).NotTo(ContainSubstring("dry-run plan"))
	}
	g.Expect(lines).To(HaveLen(1))
	g.Expect(lines[0]).NotTo(ContainSubstring("patch"))
}

func TestPlanUpdateFailsWhenCurrentObjectCannotBeRead(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	ctx := WithPlan(newLogContext(&lines), NewPlan(), source)

	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	c := NewClient(real, real)

	missing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
	err := c.Update(ctx, missing)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("dry-run plan"))
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

func TestPlanHidesSecretValues(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	plan := NewPlan()
	ctx := WithPlan(newLogContext(&lines), plan, source)

	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("old-secret-value")},
	}
	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(existing).Build()
	c := NewClient(real, real)

	current := &corev1.Secret{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(existing), current)).To(Succeed())
	current.Data["token"] = []byte("new-secret-value")
	g.Expect(c.Update(ctx, current)).To(Succeed())

	text := reportText(g, plan.Report())
	g.Expect(text).To(ContainSubstring("data.token"))
	g.Expect(text).To(ContainSubstring("sensitive"))
	g.Expect(text).NotTo(ContainSubstring("secret-value"))
	g.Expect(text).NotTo(ContainSubstring("c2VjcmV0"))
}

func TestPlanVariants(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	key := objectKey{Group: "example.com", Kind: "Mesh", Name: "mesh"}
	live := map[string]interface{}{"status": map[string]interface{}{"ready": false}}
	a := Source{Kind: "Cluster", Name: "a", Clusters: []string{"a"}}
	b := Source{Kind: "Cluster", Name: "b", Clusters: []string{"b"}}
	c := Source{Kind: "Cluster", Name: "c", Clusters: []string{"c"}}

	plan.recordStatusUpdate(a, key, live, map[string]interface{}{"status": map[string]interface{}{"ready": true}})
	plan.recordStatusUpdate(b, key, live, map[string]interface{}{"status": map[string]interface{}{"ready": true}})
	plan.recordStatusUpdate(c, key, live, map[string]interface{}{"status": map[string]interface{}{"ready": false, "reason": "x"}})

	report := plan.Report()
	g.Expect(report.Changes).To(HaveLen(1))
	g.Expect(report.Changes[0].Clusters).To(Equal([]string{"a", "b", "c"}))
	g.Expect(report.Changes[0].Variants).To(HaveLen(2))
	g.Expect(report.Changes[0].Variants[0].Sources).To(Equal([]string{"Cluster/a", "Cluster/b"}))
	g.Expect(reportText(g, report)).To(ContainSubstring(`# cluster a, b, c
~ Mesh.example.com mesh (update)
  # update from Cluster/a, Cluster/b
-     status.ready: false
+     status.ready: true
  # update from Cluster/c
+     status.reason: "x"
`))
}

func TestPlanUpdateKeepsRecordedStatus(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	key := objectKey{Kind: "Thing", Name: "thing"}
	live := map[string]interface{}{"spec": map[string]interface{}{"a": "1"}, "status": map[string]interface{}{"ready": false}}

	plan.recordStatusUpdate(source, key, live, map[string]interface{}{"spec": map[string]interface{}{"a": "1"}, "status": map[string]interface{}{"ready": true}})
	// The response of a later update of the main resource has the live status.
	plan.recordUpdate(source, key, live, map[string]interface{}{"spec": map[string]interface{}{"a": "2"}, "status": map[string]interface{}{"ready": false}})

	g.Expect(plan.Report().Changes[0].Variants[0].Diff).To(Equal([]string{
		`-     spec.a: "1"`,
		`+     spec.a: "2"`,
		`-     status.ready: false`,
		`+     status.ready: true`,
	}))
}

func TestDiffListOrder(t *testing.T) {
	g := NewWithT(t)
	tags := func(kv ...string) []interface{} {
		var l []interface{}
		for i := 0; i < len(kv); i += 2 {
			l = append(l, map[string]interface{}{"key": kv[i], "value": kv[i+1]})
		}
		return l
	}

	var lines []string
	diffValue("tags", tags("a", "1", "b", "2", "c", "3"), tags("a", "1", "b", "2", "c", "3"), &lines)
	g.Expect(lines).To(BeEmpty())

	diffValue("tags", tags("c", "3", "a", "1", "b", "2"), tags("a", "1", "b", "2", "c", "3"), &lines)
	g.Expect(lines).To(Equal([]string{"~     tags: the order of the elements changes"}))

	lines = nil
	diffValue("refs", []interface{}{"y", "x"}, []interface{}{"x", "y"}, &lines)
	g.Expect(lines).To(Equal([]string{"~     refs: the order of the elements changes"}))

	// A change of the elements is shown instead of a change of their order.
	lines = nil

	diffValue("tags", tags("a", "1", "b", "2"), tags("b", "3", "a", "1", "d", "4"), &lines)
	g.Expect(lines).To(Equal([]string{
		`-     tags[key=b].value: "2"`,
		`+     tags[key=b].value: "3"`,
		`+     tags[key=d].key: "d"`,
		`+     tags[key=d].value: "4"`,
	}))

	lines = nil
	diffValue("refs", []interface{}{"x", "y"}, []interface{}{"y", "z"}, &lines)
	g.Expect(lines).To(Equal([]string{`-     refs[]: "x"`, `+     refs[]: "z"`}))

	lines = nil
	rule := func(cidr string) interface{} {
		return map[string]interface{}{"fromPort": -1, "cidrs": []interface{}{cidr}}
	}
	diffValue("rules", []interface{}{rule("10.0.0.0/8")}, []interface{}{rule("10.0.0.0/8"), rule("172.16.0.0/12")}, &lines)
	g.Expect(lines).To(Equal([]string{`+     rules[]: {"cidrs":["172.16.0.0/12"],"fromPort":-1}`}))

	lines = nil
	diffValue("", map[string]interface{}{"metadata": map[string]interface{}{"annotations": map[string]interface{}{"crossplane.io/paused": "true"}}}, map[string]interface{}{}, &lines)
	g.Expect(lines).To(Equal([]string{`-     metadata.annotations[crossplane.io/paused]: "true"`}))
}

func TestPlanCloudChanges(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	change := func(src Source) CloudChange {
		return CloudChange{
			Source: src,
			Kind:   "Instance",
			ID:     "i-1",
			Call:   "ModifyInstanceAttribute",
			Before: map[string][]string{"securityGroups": {"sg-1"}},
			After:  map[string][]string{"securityGroups": {"sg-1", "sg-2"}},
		}
	}
	plan.RecordCloudChange(change(Source{Kind: "SecurityGroup", Name: "x", Clusters: []string{"a"}}))
	plan.RecordCloudChange(change(Source{Kind: "SecurityGroup", Name: "y", Clusters: []string{"a"}}))

	report := plan.Report()
	g.Expect(report.Cloud).To(HaveLen(1))
	g.Expect(report.Cloud[0].Sources).To(Equal([]string{"SecurityGroup/x", "SecurityGroup/y"}))
	g.Expect(report.HasChanges()).To(BeTrue())
	g.Expect(reportText(g, report)).To(ContainSubstring(`# AWS and Spot

# cluster a
~ Instance i-1 (ModifyInstanceAttribute)
-     securityGroups: [sg-1]
+     securityGroups: [sg-1, sg-2]
`))

	plan.forget(Source{Kind: "SecurityGroup", Name: "x", Clusters: []string{"a"}})
	plan.forget(Source{Kind: "SecurityGroup", Name: "y", Clusters: []string{"a"}})
	g.Expect(plan.Report().HasChanges()).To(BeFalse())
}

func TestPlanRun(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	errWaiting := errors.New("waiting")
	src := func(name string) Source { return Source{Kind: "SecurityGroup", Name: name} }
	conflicts := 0

	created := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "created-sg"}}
	plan.recordCreate(src("creator"), keyFor(schema.GroupVersionKind{Kind: "ConfigMap"}, created), map[string]interface{}{})

	targets := []Target{
		{Source: src("ok"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{RequeueAfter: time.Hour}, nil
		}},
		{Source: src("paused"), SkipReason: "paused", Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			t.Fatal("a skipped target was reconciled")
			return ctrl.Result{}, nil
		}},
		{Source: src("requeue"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}},
		{Source: src("waiting"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{}, errWaiting
		}},
		{Source: src("needs-created"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{}, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "created-sg")
		}},
		{Source: src("not-found"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{}, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "other")
		}},
		{Source: src("failed"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{}, errors.New("boom")
		}},
		{Source: src("panic"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			panic("oops")
		}},
		{Source: src("conflict"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			conflicts++
			if conflicts == 1 {
				return ctrl.Result{}, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "conflict", errors.New("changed"))
			}
			return ctrl.Result{}, nil
		}},
	}
	plan.Run(context.TODO(), 3, targets, func(err error) bool { return errors.Is(err, errWaiting) })

	statuses := map[string]Status{}
	for _, r := range plan.Report().Results {
		statuses[r.Source.Name] = r.Status
	}
	g.Expect(statuses).To(Equal(map[string]Status{
		"ok":            StatusOK,
		"paused":        StatusSkipped,
		"requeue":       StatusPending,
		"waiting":       StatusPending,
		"needs-created": StatusPending,
		"not-found":     StatusError,
		"failed":        StatusError,
		"panic":         StatusError,
		"conflict":      StatusOK,
	}))
	g.Expect(conflicts).To(Equal(2))

	report := plan.Report()
	g.Expect(report.Summary.Errors).To(Equal(3))
	g.Expect(report.HasErrors()).To(BeTrue())
	g.Expect(reportText(g, report)).To(ContainSubstring("! error SecurityGroup/failed: boom\n"))
}

func TestPlanRunStopsAtDeadline(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	ctx, cancel := context.WithCancel(context.TODO())

	targets := []Target{
		{Source: Source{Kind: "SecurityGroup", Name: "slow"}, Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			cancel()
			<-ctx.Done()
			return ctrl.Result{}, ctx.Err()
		}},
		{Source: Source{Kind: "SecurityGroup", Name: "late"}, Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			return ctrl.Result{}, nil
		}},
	}
	plan.Run(ctx, 1, targets, func(error) bool { return false })

	report := plan.Report()
	g.Expect(report.Summary.Errors).To(Equal(2))
	g.Expect(reportText(g, report)).To(ContainSubstring("! error SecurityGroup/late: not reconciled: context canceled"))
}

func TestPlanRunFailsOnIgnoredWriteErrors(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	existing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "default"}}
	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(existing).Build()
	c := NewClient(real, real)
	src := func(name string) Source { return Source{Kind: "SecurityGroup", Name: name} }

	targets := []Target{
		{Source: src("ignores-failed-patch"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			missing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
			_ = c.Patch(ctx, missing, client.MergeFrom(missing.DeepCopy()))
			return ctrl.Result{}, nil
		}},
		{Source: src("creates-existing"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			// The fake client does not return AlreadyExists for a dry-run create.
			recordWriteError(ctx, "create", existing, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, "existing"))
			return ctrl.Result{}, nil
		}},
		{Source: src("deletes-missing"), Reconcile: func(ctx context.Context) (ctrl.Result, error) {
			err := c.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}})
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}},
	}
	plan.Run(context.TODO(), 1, targets, func(error) bool { return false })

	results := map[string]Result{}
	for _, r := range plan.Report().Results {
		results[r.Source.Name] = r
	}
	g.Expect(results["ignores-failed-patch"].Status).To(Equal(StatusError))
	g.Expect(results["ignores-failed-patch"].Reason).To(ContainSubstring("a write failed: patch default/missing"))
	g.Expect(results["creates-existing"].Status).To(Equal(StatusOK))
	g.Expect(results["deletes-missing"].Status).To(Equal(StatusOK))
}

func TestPlanRunRetriesIgnoredWriteConflicts(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	attempts := 0
	target := Target{Source: Source{Kind: "SecurityGroup", Name: "sg"}, Reconcile: func(ctx context.Context) (ctrl.Result, error) {
		attempts++
		if attempts == 1 {
			recordWriteError(ctx, "patch", &corev1.ConfigMap{}, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "cm", errors.New("changed")))
		}
		return ctrl.Result{}, nil
	}}
	plan.Run(context.TODO(), 0, []Target{target}, func(error) bool { return false })

	g.Expect(attempts).To(Equal(2))
	g.Expect(plan.Report().Results[0].Status).To(Equal(StatusOK))
}

func TestPlanCloudChangeOrderIsStable(t *testing.T) {
	g := NewWithT(t)
	var outputs []string
	for i := 0; i < 20; i++ {
		plan := NewPlan()
		for _, sg := range []string{"sg-b", "sg-a", "sg-c"} {
			plan.RecordCloudChange(CloudChange{
				Source: Source{Kind: "SecurityGroup", Name: sg, Clusters: []string{"a"}},
				Kind:   "LaunchTemplate",
				ID:     "lt-1",
				Call:   "CreateLaunchTemplateVersion",
				After:  map[string][]string{"securityGroups": {sg}},
			})
		}
		outputs = append(outputs, reportText(g, plan.Report()))
	}
	for _, o := range outputs {
		g.Expect(o).To(Equal(outputs[0]))
	}
}

func TestPlanOrderOnlyAndReleasedChanges(t *testing.T) {
	g := NewWithT(t)
	plan := NewPlan()
	ref := func(name string) interface{} { return map[string]interface{}{"name": name} }

	mesh := objectKey{Group: "example.com", Kind: "Mesh", Name: "mesh"}
	plan.recordStatusUpdate(source, mesh,
		map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("b"), ref("a")}}},
		map[string]interface{}{"status": map[string]interface{}{"refs": []interface{}{ref("a"), ref("b")}}})

	pool := objectKey{Group: "example.com", Kind: "Pool", Namespace: "ns", Name: "pool"}
	live := map[string]interface{}{"metadata": map[string]interface{}{
		"name":              "pool",
		"deletionTimestamp": "2025-05-13T15:28:24Z",
		"finalizers":        []interface{}{"example.com/sg"},
	}}
	plan.recordUpdate(source, pool, live, map[string]interface{}{"metadata": map[string]interface{}{
		"name":              "pool",
		"deletionTimestamp": "2025-05-13T15:28:24Z",
	}})

	// Removing one of two finalizers does not delete the object.
	kept := objectKey{Group: "example.com", Kind: "Pool", Namespace: "ns", Name: "kept"}
	plan.recordUpdate(source, kept, map[string]interface{}{"metadata": map[string]interface{}{
		"deletionTimestamp": "2025-05-13T15:28:24Z",
		"finalizers":        []interface{}{"example.com/sg", "example.com/other"},
	}}, map[string]interface{}{"metadata": map[string]interface{}{
		"deletionTimestamp": "2025-05-13T15:28:24Z",
		"finalizers":        []interface{}{"example.com/other"},
	}})

	report := plan.Report()
	g.Expect(report.Summary).To(Equal(Summary{Update: 2, Reorder: 1, Delete: 1}))
	g.Expect(reportText(g, report)).To(Equal(`# cluster a
~ Mesh.example.com mesh (update, order only)
~     status.refs: the order of the elements changes
~ Pool.example.com ns/kept (update)
-     metadata.finalizers[]: "example.com/sg"
- Pool.example.com ns/pool (delete)
  # removes the last finalizer of an object that is being deleted, so the object is deleted
-     metadata.finalizers[]: "example.com/sg"

Plan: 0 to create, 2 to update (1 only reorder lists), 1 to delete, 0 AWS/Spot changes. Reconciled 0 objects: 0 skipped (paused), 0 pending, 0 errors.
`))
}
