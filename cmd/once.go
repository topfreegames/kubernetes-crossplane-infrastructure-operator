package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	clustermeshcontrollers "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/clustermesh.infrastructure"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/dryrun"
)

// Exit codes of --once, like terraform plan -detailed-exitcode. A plan with a
// pending reconcile is incomplete and exits with exitError.
const (
	exitNoChanges = 0
	exitError     = 1
	exitChanges   = 2
)

// planOnce reconciles every Cluster, then every SecurityGroup, once and
// records what the reconciles would change. Nothing is stored, so the
// SecurityGroup reconciles do not see the SecurityGroups that the ClusterMesh
// reconciles would create or change. When clusterName is set and no object
// matches it, planOnce fails, so that a wrong name does not plan no changes.
func planOnce(ctx context.Context, c client.Reader, plan *dryrun.Plan, clusterMeshReconciler *clustermeshcontrollers.ClusterMeshReconciler, sgReconciler *sgcontroller.SecurityGroupReconciler, clusterMeshWorkers, sgWorkers int, clusterName string) error {
	clusterTargets, err := clusterTargets(ctx, c, clusterMeshReconciler)
	if err != nil {
		return err
	}
	plan.Run(ctx, clusterMeshWorkers, clusterTargets, isPending)

	sgTargets, err := securityGroupTargets(ctx, c, sgReconciler)
	if err != nil {
		return err
	}
	plan.Run(ctx, sgWorkers, sgTargets, isPending)

	if clusterName != "" && len(clusterTargets)+len(sgTargets) == 0 {
		return fmt.Errorf("no Cluster or SecurityGroup matches --cluster=%s", clusterName)
	}
	return nil
}

func isPending(err error) bool {
	return errors.Is(err, sgcontroller.ErrSecurityGroupNotAvailable)
}

func clusterTargets(ctx context.Context, c client.Reader, r *clustermeshcontrollers.ClusterMeshReconciler) ([]dryrun.Target, error) {
	clusters := &clusterv1beta1.ClusterList{}
	if err := c.List(ctx, clusters); err != nil {
		return nil, fmt.Errorf("listing Clusters: %w", err)
	}
	var targets []dryrun.Target
	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
		if ok, err := accepts(ctx, r.RequestFilter, req); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		name := cluster.Name
		if ref := cluster.Spec.ControlPlaneRef; ref != nil {
			name = ref.Name
		}
		targets = append(targets, dryrun.Target{
			Source: dryrun.Source{Kind: "Cluster", Namespace: cluster.Namespace, Name: cluster.Name, Clusters: []string{name}},
			Reconcile: func(ctx context.Context) (ctrl.Result, error) {
				return r.Reconcile(ctx, req)
			},
		})
	}
	return targets, nil
}

func securityGroupTargets(ctx context.Context, c client.Reader, r *sgcontroller.SecurityGroupReconciler) ([]dryrun.Target, error) {
	sgs := &securitygroupv1alpha2.SecurityGroupList{}
	if err := c.List(ctx, sgs); err != nil {
		return nil, fmt.Errorf("listing SecurityGroups: %w", err)
	}
	var targets []dryrun.Target
	for i := range sgs.Items {
		sg := &sgs.Items[i]
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sg)}
		if ok, err := accepts(ctx, r.RequestFilter, req); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		clusters, err := securityGroupClusters(ctx, c, sg)
		if err != nil {
			return nil, err
		}
		t := dryrun.Target{
			Source: dryrun.Source{Kind: "SecurityGroup", Namespace: sg.Namespace, Name: sg.Name, Clusters: clusters},
			Reconcile: func(ctx context.Context) (ctrl.Result, error) {
				return r.Reconcile(ctx, req)
			},
		}
		// The reconciler fails on a SecurityGroup without infrastructureRef
		// before it checks the pause annotation.
		if sg.Spec.InfrastructureRef != nil && sg.GetAnnotations()[sgcontroller.AnnotationKeyReconciliationPaused] == "true" {
			t.SkipReason = "paused"
		}
		targets = append(targets, t)
	}
	return targets, nil
}

func accepts(ctx context.Context, filter func(context.Context, ctrl.Request) (bool, error), req ctrl.Request) (bool, error) {
	if filter == nil {
		return true, nil
	}
	return filter(ctx, req)
}

// securityGroupClusters returns the names of the KopsControlPlanes that the
// infrastructureRefs of sg point to.
func securityGroupClusters(ctx context.Context, c client.Reader, sg *securitygroupv1alpha2.SecurityGroup) ([]string, error) {
	names := map[string]bool{}
	refs := append(append([]*corev1.ObjectReference{}, sg.Spec.InfrastructureRef...), sg.Status.AppliedInfrastructureRef...)
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		switch ref.Kind {
		case "KopsControlPlane":
			names[ref.Name] = true
		case "KopsMachinePool":
			kmp := &kinfrastructurev1alpha1.KopsMachinePool{}
			err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, kmp)
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("reading KopsMachinePool %s/%s: %w", ref.Namespace, ref.Name, err)
			}
			names[kmp.Spec.ClusterName] = true
		}
	}
	clusters := make([]string, 0, len(names))
	for name := range names {
		clusters = append(clusters, name)
	}
	sort.Strings(clusters)
	return clusters, nil
}

// writePlan writes the plan to out, and as JSON to jsonPath when it is set,
// and returns the exit code.
func writePlan(plan *dryrun.Plan, runErr error, out io.Writer, jsonPath string) int {
	report := plan.Report()
	if err := report.WriteText(out); err != nil {
		setupLog.Error(err, "unable to write the plan")
		return exitError
	}
	if jsonPath != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(jsonPath, append(data, '\n'), 0o644)
		}
		if err != nil {
			setupLog.Error(err, "unable to write the plan", "path", jsonPath)
			return exitError
		}
	}
	switch {
	case runErr != nil:
		setupLog.Error(runErr, "plan failed")
		return exitError
	case report.HasErrors() || report.Summary.Pending > 0:
		// A pending reconcile stopped before its later steps, so the plan
		// does not show all the changes.
		return exitError
	case report.HasChanges():
		return exitChanges
	default:
		return exitNoChanges
	}
}
