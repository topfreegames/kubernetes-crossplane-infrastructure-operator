package dryrun

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	clustermeshcontrollers "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/clustermesh.infrastructure"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
)

// target is an object to reconcile once.
type target struct {
	source     Source
	skipReason string
	reconcile  func(ctx context.Context) (ctrl.Result, error)
}

// Once reconciles every Cluster with the ClusterMesh reconciler, then every
// SecurityGroup with the SecurityGroup reconciler, once, and records what the
// reconciles would change in p. The reconcilers must be configured for dry
// run. Nothing is stored, so the SecurityGroup reconciles do not see the
// SecurityGroups that the ClusterMesh reconciles would create or change.
// When clusterName is set and no Cluster matches it, Once fails, so that a
// wrong name does not plan no changes.
func (p *Plan) Once(ctx context.Context, c client.Reader, cm *clustermeshcontrollers.ClusterMeshReconciler, sg *sgcontroller.SecurityGroupReconciler, cmWorkers, sgWorkers int, clusterName string) error {
	clusters := &clusterv1beta1.ClusterList{}
	if err := c.List(ctx, clusters); err != nil {
		return fmt.Errorf("listing Clusters: %w", err)
	}
	var clusterTargets []target
	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
		ok, err := accepts(ctx, cm.RequestFilter, req)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		name := cluster.Name
		if ref := cluster.Spec.ControlPlaneRef; ref != nil {
			name = ref.Name
		}
		clusterTargets = append(clusterTargets, target{
			source:    Source{Kind: "Cluster", Namespace: cluster.Namespace, Name: cluster.Name, Clusters: []string{name}},
			reconcile: func(ctx context.Context) (ctrl.Result, error) { return cm.Reconcile(ctx, req) },
		})
	}
	// The SecurityGroup filter also accepts the SecurityGroups of deleted
	// KopsMachinePools, so only a Cluster shows that the name is right.
	if clusterName != "" && len(clusterTargets) == 0 {
		return fmt.Errorf("no Cluster matches --cluster=%s", clusterName)
	}
	p.run(ctx, cmWorkers, clusterTargets)

	sgs := &securitygroupv1alpha2.SecurityGroupList{}
	if err := c.List(ctx, sgs); err != nil {
		return fmt.Errorf("listing SecurityGroups: %w", err)
	}
	var sgTargets []target
	for i := range sgs.Items {
		obj := &sgs.Items[i]
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
		ok, err := accepts(ctx, sg.RequestFilter, req)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		clusters, err := securityGroupClusters(ctx, c, obj)
		if err != nil {
			return err
		}
		t := target{
			source:    Source{Kind: "SecurityGroup", Namespace: obj.Namespace, Name: obj.Name, Clusters: clusters},
			reconcile: func(ctx context.Context) (ctrl.Result, error) { return sg.Reconcile(ctx, req) },
		}
		// The reconciler fails on a SecurityGroup without infrastructureRef
		// before it checks the pause annotation.
		if obj.Spec.InfrastructureRef != nil && obj.GetAnnotations()[sgcontroller.AnnotationKeyReconciliationPaused] == "true" {
			t.skipReason = "paused"
		}
		sgTargets = append(sgTargets, t)
	}
	p.run(ctx, sgWorkers, sgTargets)
	return nil
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
	return sortedKeys(names), nil
}

// run reconciles each target once with the given number of workers. Targets
// that are not reconciled before ctx ends are errors.
func (p *Plan) run(ctx context.Context, workers int, targets []target) {
	queue := make(chan target)
	var wg sync.WaitGroup
	for i := 0; i < max(workers, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range queue {
				p.reconcile(ctx, t)
			}
		}()
	}
	for _, t := range targets {
		if t.skipReason != "" {
			p.SetResult(t.source, StatusSkipped, t.skipReason)
			continue
		}
		if ctx.Err() == nil {
			select {
			case queue <- t:
				continue
			case <-ctx.Done():
			}
		}
		p.SetResult(t.source, StatusError, fmt.Sprintf("not reconciled: %v", ctx.Err()))
	}
	close(queue)
	wg.Wait()
}

// reconcile reconciles t and records its outcome. A reconcile that asks to
// run again within an hour, or that waits for a SecurityGroup or for an object
// that this plan creates, is pending: the real controller would retry it.
func (p *Plan) reconcile(ctx context.Context, t target) {
	log := ctrl.LoggerFrom(ctx).WithValues(t.source.Kind, t.source.String())
	ctx = WithPlan(ctrl.LoggerInto(ctx, log), p, t.source)

	result, err := p.reconcileOnce(ctx, t)
	if apierrors.IsConflict(err) {
		// The object changed since the cache read it, most likely because the
		// running operator wrote it. Read it again.
		log.Info("conflict, reconciling again", "error", err.Error())
		p.forget(t.source)
		result, err = p.reconcileOnce(ctx, t)
	}

	switch {
	case errors.Is(err, sgcontroller.ErrSecurityGroupNotAvailable):
		p.SetResult(t.source, StatusPending, err.Error())
	case apierrors.IsNotFound(err) && p.created(notFoundObject(err)):
		p.SetResult(t.source, StatusPending, fmt.Sprintf("waits for an object that this plan creates: %v", err))
	case err != nil:
		p.SetResult(t.source, StatusError, err.Error())
	case result.RequeueAfter > 0 && result.RequeueAfter < time.Hour:
		p.SetResult(t.source, StatusPending, fmt.Sprintf("would run again in %s", result.RequeueAfter))
	default:
		p.SetResult(t.source, StatusOK, "")
	}
	if err != nil {
		log.Error(err, "reconcile failed")
	}
}

// reconcileOnce reconciles t. When the reconcile succeeds but one of its
// writes failed, for example a finalizer patch whose error the reconciler
// ignores, it returns that write error.
func (p *Plan) reconcileOnce(ctx context.Context, t target) (result ctrl.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
		if err == nil {
			if werr := p.writeError(t.source); werr != nil {
				err = fmt.Errorf("a write failed: %w", werr)
			}
		}
	}()
	return t.reconcile(ctx)
}

// notFoundObject returns the API group and the name of the object that a
// NotFound error is about.
func notFoundObject(err error) (string, string) {
	var status apierrors.APIStatus
	if errors.As(err, &status) && status.Status().Details != nil {
		return status.Status().Details.Group, status.Status().Details.Name
	}
	return "", ""
}
