package dryrun

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewEventRecorder returns a recorder that logs events instead of creating
// them.
func NewEventRecorder(log logr.Logger) record.EventRecorder {
	return &eventRecorder{log: log.WithName("dry-run")}
}

type eventRecorder struct {
	log logr.Logger
}

func (r *eventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	kv := []interface{}{"type", eventtype, "reason", reason, "message", message}
	if o, ok := object.(client.Object); ok {
		kv = append(kv, "namespace", o.GetNamespace(), "name", o.GetName())
	}
	r.log.Info("would record event", kv...)
}

func (r *eventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	r.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (r *eventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	r.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}
