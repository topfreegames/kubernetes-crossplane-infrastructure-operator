package dryrun

import (
	"context"
	"testing"

	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newLogContext(lines *[]string) context.Context {
	log := funcr.New(func(prefix, args string) {
		*lines = append(*lines, prefix+" "+args)
	}, funcr.Options{})
	return ctrl.LoggerInto(context.TODO(), log)
}

func TestClientWritesAreNotPersisted(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	ctx := newLogContext(&lines)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "default"},
		Data:       map[string]string{"key": "old"},
	}
	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(existing).Build()
	c := NewClient(real)

	created := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "created", Namespace: "default"}}
	g.Expect(c.Create(ctx, created)).To(Succeed())
	err := real.Get(ctx, client.ObjectKeyFromObject(created), &corev1.ConfigMap{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

	updated := existing.DeepCopy()
	updated.Data["key"] = "new"
	g.Expect(c.Update(ctx, updated)).To(Succeed())

	patched := existing.DeepCopy()
	patched.Finalizers = []string{"example.com/finalizer"}
	g.Expect(c.Patch(ctx, patched, client.MergeFrom(existing))).To(Succeed())

	g.Expect(c.Delete(ctx, existing.DeepCopy())).To(Succeed())

	current := &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(existing), current)).To(Succeed())
	g.Expect(current.Data["key"]).To(Equal("old"))
	g.Expect(current.Finalizers).To(BeEmpty())

	g.Expect(lines).To(HaveLen(4))
	g.Expect(lines[0]).To(And(ContainSubstring(`"verb"="create"`), ContainSubstring(`"kind"="ConfigMap"`), ContainSubstring(`"name"="created"`)))
	g.Expect(lines[1]).To(And(ContainSubstring(`"verb"="update"`), ContainSubstring(`"name"="existing"`), ContainSubstring(`\"key\":\"new\"`)))
	g.Expect(lines[2]).To(And(ContainSubstring(`"verb"="patch"`), ContainSubstring(`example.com/finalizer`)))
	g.Expect(lines[3]).To(And(ContainSubstring(`"verb"="delete"`), ContainSubstring(`"name"="existing"`)))
}

func TestClientStatusWritesAreNotPersisted(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	ctx := newLogContext(&lines)

	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	real := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(existing).WithStatusSubresource(existing).Build()
	c := NewClient(real)

	updated := existing.DeepCopy()
	updated.Status.Phase = corev1.PodRunning
	g.Expect(c.Status().Update(ctx, updated)).To(Succeed())

	current := &corev1.Pod{}
	g.Expect(real.Get(ctx, client.ObjectKeyFromObject(existing), current)).To(Succeed())
	g.Expect(current.Status.Phase).To(Equal(corev1.PodPending))

	g.Expect(lines).To(HaveLen(1))
	g.Expect(lines[0]).To(And(ContainSubstring(`"verb"="update"`), ContainSubstring(`"subresource"="status"`), ContainSubstring(`Running`)))
}

func TestEventRecorder(t *testing.T) {
	g := NewWithT(t)
	var lines []string
	log := funcr.New(func(prefix, args string) {
		lines = append(lines, prefix+" "+args)
	}, funcr.Options{})

	r := NewEventRecorder(log)
	r.Eventf(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm"}}, corev1.EventTypeWarning, "Reason", "failed %s", "here")

	g.Expect(lines).To(HaveLen(1))
	g.Expect(lines[0]).To(And(ContainSubstring("would record event"), ContainSubstring(`"reason"="Reason"`), ContainSubstring(`"message"="failed here"`), ContainSubstring(`"name"="cm"`)))
}
