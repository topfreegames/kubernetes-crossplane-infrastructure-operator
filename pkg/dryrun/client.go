package dryrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewClient returns a client that sends every write to the API server with
// dryRun=All and logs it. Reads go to c unchanged. apiReader reads the current
// object to log the change an update would make.
func NewClient(c client.Client, apiReader client.Reader) client.Client {
	return &loggingClient{
		Client: client.NewDryRunClient(c),
		reader: apiReader,
	}
}

type loggingClient struct {
	client.Client
	reader client.Reader
}

func logger(ctx context.Context) logr.Logger {
	return ctrl.LoggerFrom(ctx).WithName("dry-run")
}

// isSecret reports whether obj is a Secret, whose payload must not be logged.
func (c *loggingClient) isSecret(obj runtime.Object) bool {
	gvk, err := c.GroupVersionKindFor(obj)
	return err == nil && gvk.Group == "" && gvk.Kind == "Secret"
}

func (c *loggingClient) describe(obj runtime.Object) []interface{} {
	kind := ""
	if gvk, err := c.GroupVersionKindFor(obj); err == nil {
		kind = gvk.Kind
	}
	kv := []interface{}{"kind", kind}
	if o, ok := obj.(client.Object); ok {
		kv = append(kv, "namespace", o.GetNamespace(), "name", o.GetName())
	}
	return kv
}

// current reads the object currently in the cluster with the key of obj.
func (c *loggingClient) current(ctx context.Context, obj client.Object) (client.Object, error) {
	gvk, err := c.GroupVersionKindFor(obj)
	if err != nil {
		return nil, err
	}
	o, err := c.Scheme().New(gvk)
	if err != nil {
		return nil, err
	}
	current, ok := o.(client.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a client.Object", gvk)
	}
	if err := c.reader.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
		return nil, err
	}
	current.GetObjectKind().SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())
	return current, nil
}

// diff returns a JSON merge patch from current to obj, or nil when current is
// nil.
func diff(current, obj client.Object) []byte {
	if current == nil {
		return nil
	}
	data, err := client.MergeFrom(current).Data(obj)
	if err != nil {
		return nil
	}
	return data
}

// currentForPlan reads the object currently in the cluster when ctx records a
// plan. The plan needs it, so an error is returned; without a plan it is only
// used for the log, so the error is dropped.
func (c *loggingClient) currentForPlan(ctx context.Context, obj client.Object) (client.Object, error) {
	current, err := c.current(ctx, obj)
	if err != nil {
		if _, _, ok := planFrom(ctx); ok {
			return nil, fmt.Errorf("dry-run plan: reading the current %s %s: %w", obj.GetObjectKind().GroupVersionKind().Kind, client.ObjectKeyFromObject(obj), err)
		}
		return nil, nil
	}
	return current, nil
}

// recordWriteError adds a failed write to the plan of ctx. Reconcilers ignore
// some write errors, for example a failed finalizer patch, so the plan must
// see them itself. An existing object on create and a missing object on
// delete are not errors: the reconcilers expect them.
func recordWriteError(ctx context.Context, verb string, obj client.Object, err error) {
	if err == nil {
		return
	}
	if verb == "create" && apierrors.IsAlreadyExists(err) || verb == "delete" && apierrors.IsNotFound(err) {
		return
	}
	p, source, ok := planFrom(ctx)
	if !ok {
		return
	}
	p.recordWriteError(source, fmt.Errorf("%s %s: %w", verb, client.ObjectKeyFromObject(obj), err))
}

type recordFunc func(p *Plan, source Source, key objectKey, live, obj map[string]interface{})

// record adds a write of obj that the API server accepted to the plan of ctx.
func (c *loggingClient) record(ctx context.Context, obj, live client.Object, f recordFunc) error {
	p, source, ok := planFrom(ctx)
	if !ok {
		return nil
	}
	gvk, err := c.GroupVersionKindFor(obj)
	if err != nil {
		return fmt.Errorf("dry-run plan: %w", err)
	}
	desired, err := c.planMap(obj)
	if err != nil {
		return err
	}
	var liveMap map[string]interface{}
	if live != nil {
		if liveMap, err = c.planMap(live); err != nil {
			return err
		}
	}
	f(p, source, keyFor(gvk, obj), liveMap, desired)
	return nil
}

func (c *loggingClient) planMap(obj client.Object) (map[string]interface{}, error) {
	m, err := toMap(obj, c.isSecret(obj))
	if err != nil {
		return nil, fmt.Errorf("dry-run plan: converting %s: %w", client.ObjectKeyFromObject(obj), err)
	}
	return m, nil
}

func (c *loggingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) (err error) {
	defer func() { recordWriteError(ctx, "create", obj, err) }()
	kv := append([]interface{}{"verb", "create"}, c.describe(obj)...)
	if data, err := json.Marshal(obj); err == nil && !c.isSecret(obj) {
		kv = append(kv, "object", string(data))
	}
	logger(ctx).Info("would write", kv...)
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	return c.record(ctx, obj, nil, (*Plan).recordCreate)
}

func (c *loggingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) (err error) {
	defer func() { recordWriteError(ctx, "update", obj, err) }()
	current, err := c.currentForPlan(ctx, obj)
	if err != nil {
		return err
	}
	kv := append([]interface{}{"verb", "update"}, c.describe(obj)...)
	if data := diff(current, obj); data != nil && !c.isSecret(obj) {
		kv = append(kv, "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return c.record(ctx, obj, current, (*Plan).recordUpdate)
}

func (c *loggingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) (err error) {
	defer func() { recordWriteError(ctx, "patch", obj, err) }()
	kv := append([]interface{}{"verb", "patch"}, c.describe(obj)...)
	if data, err := patch.Data(obj); err == nil && !c.isSecret(obj) {
		kv = append(kv, "patchType", string(patch.Type()), "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	var current client.Object
	if _, _, ok := planFrom(ctx); ok {
		var err error
		if current, err = c.currentForPlan(ctx, obj); err != nil {
			return err
		}
	}
	if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	return c.record(ctx, obj, current, (*Plan).recordUpdate)
}

func (c *loggingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) (err error) {
	defer func() { recordWriteError(ctx, "delete", obj, err) }()
	logger(ctx).Info("would write", append([]interface{}{"verb", "delete"}, c.describe(obj)...)...)
	if err := c.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	if _, _, ok := planFrom(ctx); !ok {
		return nil
	}
	// The plan needs the current object to tell whether it is already being
	// deleted.
	current, err := c.currentForPlan(ctx, obj)
	if err != nil {
		return err
	}
	return c.record(ctx, obj, current, (*Plan).recordDelete)
}

func (c *loggingClient) Status() client.SubResourceWriter {
	return c.SubResource("status")
}

func (c *loggingClient) SubResource(subResource string) client.SubResourceClient {
	return &loggingSubResourceClient{
		SubResourceClient: c.Client.SubResource(subResource),
		parent:            c,
		subResource:       subResource,
	}
}

type loggingSubResourceClient struct {
	client.SubResourceClient
	parent      *loggingClient
	subResource string
}

// recordFunc returns how a write of the subresource is added to the plan.
func (c *loggingSubResourceClient) recordFunc() recordFunc {
	if c.subResource == "status" {
		return (*Plan).recordStatusUpdate
	}
	return (*Plan).recordUpdate
}

func (c *loggingSubResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) (err error) {
	defer func() { recordWriteError(ctx, "update "+c.subResource, obj, err) }()
	current, err := c.parent.currentForPlan(ctx, obj)
	if err != nil {
		return err
	}
	kv := append([]interface{}{"verb", "update", "subresource", c.subResource}, c.parent.describe(obj)...)
	if data := diff(current, obj); data != nil {
		kv = append(kv, "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	if err := c.SubResourceClient.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return c.parent.record(ctx, obj, current, c.recordFunc())
}

func (c *loggingSubResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) (err error) {
	defer func() { recordWriteError(ctx, "patch "+c.subResource, obj, err) }()
	kv := append([]interface{}{"verb", "patch", "subresource", c.subResource}, c.parent.describe(obj)...)
	if data, err := patch.Data(obj); err == nil {
		kv = append(kv, "patchType", string(patch.Type()), "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	var current client.Object
	if _, _, ok := planFrom(ctx); ok {
		var err error
		if current, err = c.parent.currentForPlan(ctx, obj); err != nil {
			return err
		}
	}
	if err := c.SubResourceClient.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	return c.parent.record(ctx, obj, current, c.recordFunc())
}
