package clusterfilter

import (
	"context"

	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
)

// ClusterRequestFilter accepts the Cluster whose control plane is the
// KopsControlPlane named clusterName.
func ClusterRequestFilter(c client.Reader, clusterName string) func(ctx context.Context, req ctrl.Request) (bool, error) {
	return func(ctx context.Context, req ctrl.Request) (bool, error) {
		cluster := &clusterv1beta1.Cluster{}
		if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		return cluster.Spec.ControlPlaneRef != nil && cluster.Spec.ControlPlaneRef.Name == clusterName, nil
	}
}

// SecurityGroupRequestFilter accepts the SecurityGroups whose spec or applied
// infrastructureRef points to the KopsControlPlane named clusterName, to a
// KopsMachinePool of that cluster, or to a KopsMachinePool that no longer
// exists and so cannot be matched to a cluster.
func SecurityGroupRequestFilter(c client.Reader, clusterName string) func(ctx context.Context, req ctrl.Request) (bool, error) {
	return func(ctx context.Context, req ctrl.Request) (bool, error) {
		sg := &securitygroupv1alpha2.SecurityGroup{}
		if err := c.Get(ctx, req.NamespacedName, sg); err != nil {
			return false, client.IgnoreNotFound(err)
		}

		refs := append([]*corev1.ObjectReference{}, sg.Spec.InfrastructureRef...)
		refs = append(refs, sg.Status.AppliedInfrastructureRef...)
		for _, ref := range refs {
			if ref == nil {
				continue
			}
			switch ref.Kind {
			case "KopsControlPlane":
				if ref.Name == clusterName {
					return true, nil
				}
			case "KopsMachinePool":
				kmp := &kinfrastructurev1alpha1.KopsMachinePool{}
				err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, kmp)
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				if err != nil {
					return false, err
				}
				if kmp.Spec.ClusterName == clusterName {
					return true, nil
				}
			}
		}
		return false, nil
	}
}
