package dryrun

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PendingAfter is the shortest requeue of a reconcile that finished its work.
// A reconcile that asks to run again sooner waits for something, for example a
// Crossplane resource to become ready.
const PendingAfter = time.Hour

// Target is an object to reconcile once.
type Target struct {
	Source Source
	// SkipReason, when set, skips the reconcile.
	SkipReason string
	Reconcile  func(ctx context.Context) (ctrl.Result, error)
}

// Run reconciles each target once with the given number of workers and
// records the writes and the outcome of each reconcile in p. isPending
// reports whether a reconcile error means that the reconcile waits for
// something instead of failing. Targets that are not reconciled before ctx
// ends are errors.
func (p *Plan) Run(ctx context.Context, workers int, targets []Target, isPending func(error) bool) {
	if workers < 1 {
		workers = 1
	}
	queue := make(chan Target)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range queue {
				p.reconcile(ctx, t, isPending)
			}
		}()
	}
	for _, t := range targets {
		if t.SkipReason != "" {
			p.SetResult(t.Source, StatusSkipped, t.SkipReason)
			continue
		}
		if ctx.Err() == nil {
			select {
			case queue <- t:
				continue
			case <-ctx.Done():
			}
		}
		p.SetResult(t.Source, StatusError, fmt.Sprintf("not reconciled: %v", ctx.Err()))
	}
	close(queue)
	wg.Wait()
}

func (p *Plan) reconcile(ctx context.Context, t Target, isPending func(error) bool) {
	log := ctrl.LoggerFrom(ctx).WithValues(t.Source.Kind, t.Source.String())
	ctx = WithPlan(ctrl.LoggerInto(ctx, log), p, t.Source)

	result, err := p.reconcileAndCheckWrites(ctx, t)
	if apierrors.IsConflict(err) {
		// The object changed since the cache read it, most likely because the
		// running operator wrote it. Read it again.
		log.Info("conflict, reconciling again", "error", err.Error())
		p.forget(t.Source)
		result, err = p.reconcileAndCheckWrites(ctx, t)
	}

	switch {
	case err != nil && isPending(err):
		p.SetResult(t.Source, StatusPending, err.Error())
	case err != nil && apierrors.IsNotFound(err) && p.createdName(notFoundObject(err)):
		p.SetResult(t.Source, StatusPending, fmt.Sprintf("waits for an object that this plan creates: %v", err))
	case err != nil:
		p.SetResult(t.Source, StatusError, err.Error())
	case result.RequeueAfter > 0 && result.RequeueAfter < PendingAfter:
		p.SetResult(t.Source, StatusPending, fmt.Sprintf("would run again in %s", result.RequeueAfter))
	default:
		p.SetResult(t.Source, StatusOK, "")
	}
	if err != nil {
		log.Error(err, "reconcile failed")
	}
}

// reconcileAndCheckWrites reconciles t and returns the first failed write of
// the reconcile when the reconcile itself does not fail.
func (p *Plan) reconcileAndCheckWrites(ctx context.Context, t Target) (ctrl.Result, error) {
	result, err := safeReconcile(ctx, t)
	if err == nil {
		if werr := p.writeError(t.Source); werr != nil {
			err = fmt.Errorf("a write failed: %w", werr)
		}
	}
	return result, err
}

func safeReconcile(ctx context.Context, t Target) (result ctrl.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return t.Reconcile(ctx)
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
