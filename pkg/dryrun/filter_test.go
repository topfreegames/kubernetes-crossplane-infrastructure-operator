package dryrun

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
)

func newFilterScheme(g *WithT) *runtime.Scheme {
	s := runtime.NewScheme()
	g.Expect(clusterv1beta1.AddToScheme(s)).To(Succeed())
	g.Expect(kinfrastructurev1alpha1.AddToScheme(s)).To(Succeed())
	g.Expect(kcontrolplanev1alpha1.AddToScheme(s)).To(Succeed())
	g.Expect(securitygroupv1alpha2.AddToScheme(s)).To(Succeed())
	return s
}

func TestClusterRequestFilter(t *testing.T) {
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(newFilterScheme(g)).WithObjects(
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "other-name", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "cluster-a"}},
		},
		&clusterv1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "ns"},
			Spec:       clusterv1beta1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{Kind: "KopsControlPlane", Name: "cluster-b"}},
		},
		&clusterv1beta1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "no-control-plane", Namespace: "ns"}},
	).Build()
	filter := ClusterRequestFilter(c, "cluster-a")

	testCases := []struct {
		description string
		name        string
		expected    bool
	}{
		{"should accept the Cluster by control plane name", "other-name", true},
		{"should reject a Cluster with another control plane", "cluster-a", false},
		{"should reject a Cluster without control plane", "no-control-plane", false},
		{"should reject a missing Cluster", "missing", false},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			g := NewWithT(t)
			ok, err := filter(context.TODO(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: tc.name}})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ok).To(Equal(tc.expected))
		})
	}
}

func TestSecurityGroupRequestFilter(t *testing.T) {
	g := NewWithT(t)
	kcpRef := func(name string) *corev1.ObjectReference {
		return &corev1.ObjectReference{Kind: "KopsControlPlane", Namespace: "ns", Name: name}
	}
	kmpRef := func(name string) *corev1.ObjectReference {
		return &corev1.ObjectReference{Kind: "KopsMachinePool", Namespace: "ns", Name: name}
	}
	sg := func(name string, spec, applied []*corev1.ObjectReference) *securitygroupv1alpha2.SecurityGroup {
		return &securitygroupv1alpha2.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       securitygroupv1alpha2.SecurityGroupSpec{InfrastructureRef: spec},
			Status:     securitygroupv1alpha2.SecurityGroupStatus{AppliedInfrastructureRef: applied},
		}
	}
	c := fake.NewClientBuilder().WithScheme(newFilterScheme(g)).WithObjects(
		&kinfrastructurev1alpha1.KopsMachinePool{
			ObjectMeta: metav1.ObjectMeta{Name: "kmp-a", Namespace: "ns"},
			Spec:       kinfrastructurev1alpha1.KopsMachinePoolSpec{ClusterName: "cluster-a"},
		},
		&kinfrastructurev1alpha1.KopsMachinePool{
			ObjectMeta: metav1.ObjectMeta{Name: "kmp-b", Namespace: "ns"},
			Spec:       kinfrastructurev1alpha1.KopsMachinePoolSpec{ClusterName: "cluster-b"},
		},
		sg("sg-kcp", []*corev1.ObjectReference{kcpRef("cluster-b"), kcpRef("cluster-a")}, nil),
		sg("sg-kmp", []*corev1.ObjectReference{kmpRef("kmp-b"), kmpRef("kmp-a")}, nil),
		sg("sg-applied", []*corev1.ObjectReference{kcpRef("cluster-b")}, []*corev1.ObjectReference{kcpRef("cluster-a")}),
		sg("sg-missing-kmp", []*corev1.ObjectReference{kcpRef("cluster-b"), kmpRef("kmp-missing")}, nil),
		sg("sg-other", []*corev1.ObjectReference{kcpRef("cluster-b"), kmpRef("kmp-b")}, nil),
	).Build()
	filter := SecurityGroupRequestFilter(c, "cluster-a")

	testCases := []struct {
		description string
		name        string
		expected    bool
	}{
		{"should accept a SecurityGroup that references the KopsControlPlane", "sg-kcp", true},
		{"should accept a SecurityGroup that references a KopsMachinePool of the cluster", "sg-kmp", true},
		{"should accept a SecurityGroup that was applied to the cluster", "sg-applied", true},
		{"should accept a SecurityGroup that references a missing KopsMachinePool", "sg-missing-kmp", true},
		{"should reject a SecurityGroup of other clusters", "sg-other", false},
		{"should reject a missing SecurityGroup", "missing", false},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			g := NewWithT(t)
			ok, err := filter(context.TODO(), ctrl.Request{NamespacedName: client.ObjectKey{Name: tc.name}})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ok).To(Equal(tc.expected))
		})
	}
}
