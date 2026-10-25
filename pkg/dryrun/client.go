package dryrun

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
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

// diff returns a JSON merge patch from the object currently in the cluster to
// obj, or nil when the current object cannot be read.
func (c *loggingClient) diff(ctx context.Context, obj client.Object) []byte {
	current, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil
	}
	if err := c.reader.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
		return nil
	}
	data, err := client.MergeFrom(current).Data(obj)
	if err != nil {
		return nil
	}
	return data
}

func (c *loggingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	kv := append([]interface{}{"verb", "create"}, c.describe(obj)...)
	if data, err := json.Marshal(obj); err == nil {
		kv = append(kv, "object", string(data))
	}
	logger(ctx).Info("would write", kv...)
	return c.Client.Create(ctx, obj, opts...)
}

func (c *loggingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	kv := append([]interface{}{"verb", "update"}, c.describe(obj)...)
	if data := c.diff(ctx, obj); data != nil {
		kv = append(kv, "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	return c.Client.Update(ctx, obj, opts...)
}

func (c *loggingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	kv := append([]interface{}{"verb", "patch"}, c.describe(obj)...)
	if data, err := patch.Data(obj); err == nil {
		kv = append(kv, "patchType", string(patch.Type()), "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *loggingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	logger(ctx).Info("would write", append([]interface{}{"verb", "delete"}, c.describe(obj)...)...)
	return c.Client.Delete(ctx, obj, opts...)
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

func (c *loggingSubResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	kv := append([]interface{}{"verb", "update", "subresource", c.subResource}, c.parent.describe(obj)...)
	if data := c.parent.diff(ctx, obj); data != nil {
		kv = append(kv, "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	return c.SubResourceClient.Update(ctx, obj, opts...)
}

func (c *loggingSubResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	kv := append([]interface{}{"verb", "patch", "subresource", c.subResource}, c.parent.describe(obj)...)
	if data, err := patch.Data(obj); err == nil {
		kv = append(kv, "patchType", string(patch.Type()), "patch", string(data))
	}
	logger(ctx).Info("would write", kv...)
	return c.SubResourceClient.Patch(ctx, obj, patch, opts...)
}
