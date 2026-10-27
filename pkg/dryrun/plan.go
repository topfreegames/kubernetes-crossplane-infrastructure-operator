package dryrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/pmezard/go-difflib/difflib"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Source is the object whose reconcile makes a write.
type Source struct {
	Kind      string
	Namespace string
	Name      string
	Clusters  []string
}

func (s Source) String() string {
	if s.Namespace == "" {
		return s.Kind + "/" + s.Name
	}
	return s.Kind + "/" + s.Namespace + "/" + s.Name
}

// Status is the outcome of the reconcile of one object.
type Status string

const (
	StatusOK      Status = "ok"
	StatusSkipped Status = "skipped"
	StatusPending Status = "pending"
	StatusError   Status = "error"
)

// Plan collects the writes that reconciles would make and the outcome of each
// reconcile. A context made with WithPlan carries the plan and the source of
// the reconcile; writes made with a context without a plan are only logged.
type Plan struct {
	mu sync.Mutex
	// objects has, for each object, what each source would write to it.
	objects map[objectKey]map[string]*write
	// cloud has the AWS and Spot changes by their text.
	cloud       map[string]*cloudChange
	results     map[string]result
	writeErrors map[string]error
}

// NewPlan returns an empty plan.
func NewPlan() *Plan {
	return &Plan{
		objects:     map[objectKey]map[string]*write{},
		cloud:       map[string]*cloudChange{},
		results:     map[string]result{},
		writeErrors: map[string]error{},
	}
}

type objectKey struct {
	Group, Kind, Namespace, Name string
}

func (k objectKey) String() string {
	kind := k.Kind
	if k.Group != "" {
		kind += "." + k.Group
	}
	if k.Namespace == "" {
		return kind + " " + k.Name
	}
	return kind + " " + k.Namespace + "/" + k.Name
}

func keyFor(gvk schema.GroupVersionKind, obj client.Object) objectKey {
	return objectKey{Group: gvk.Group, Kind: gvk.Kind, Namespace: obj.GetNamespace(), Name: obj.GetName()}
}

// write is what one source would write to one object: desired replaces
// live, or the object is created or deleted.
type write struct {
	source  Source
	created bool
	deleted bool
	live    map[string]interface{}
	desired map[string]interface{}
}

type cloudChange struct {
	lines   []string
	sources []Source
}

type result struct {
	source Source
	status Status
	reason string
}

type planContextKey struct{}

type planContext struct {
	plan   *Plan
	source Source
}

// WithPlan returns a context that records the writes made with it in p, as
// writes of source.
func WithPlan(ctx context.Context, p *Plan, source Source) context.Context {
	return context.WithValue(ctx, planContextKey{}, planContext{plan: p, source: source})
}

func planFrom(ctx context.Context) (*Plan, Source, bool) {
	pc, ok := ctx.Value(planContextKey{}).(planContext)
	if !ok || pc.plan == nil {
		return nil, Source{}, false
	}
	return pc.plan, pc.source, true
}

// writeFor returns the write of source to key, starting from live.
func (p *Plan) writeFor(key objectKey, source Source, live map[string]interface{}) *write {
	bySource, ok := p.objects[key]
	if !ok {
		bySource = map[string]*write{}
		p.objects[key] = bySource
	}
	w, ok := bySource[source.String()]
	if !ok {
		w = &write{source: source, live: live, desired: deepCopy(live)}
		bySource[source.String()] = w
	}
	return w
}

func (p *Plan) recordCreate(source Source, key objectKey, _, obj map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.writeFor(key, source, nil)
	w.created = true
	w.desired = obj
}

// recordUpdate records a write of the main resource. The status of obj is
// ignored, like the API server does for resources with a status subresource.
func (p *Plan) recordUpdate(source Source, key objectKey, live, obj map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.writeFor(key, source, live)
	desired := deepCopy(obj)
	if status, ok := w.desired["status"]; ok {
		desired["status"] = status
	} else {
		delete(desired, "status")
	}
	w.desired = desired
}

func (p *Plan) recordStatusUpdate(source Source, key objectKey, live, obj map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.writeFor(key, source, live)
	if w.desired == nil {
		w.desired = map[string]interface{}{}
	}
	if status, ok := obj["status"]; ok {
		w.desired["status"] = status
	} else {
		delete(w.desired, "status")
	}
}

func (p *Plan) recordDelete(source Source, key objectKey, live, _ map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeFor(key, source, live).deleted = true
}

// RecordCloudChange records a write to AWS or Spot as diff lines: a header
// that starts with "~ ", then the old values with "-" and the new values with
// "+" in the first column. The same change from several sources is kept once.
func (p *Plan) RecordCloudChange(source Source, lines ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := strings.Join(lines, "\n")
	c, ok := p.cloud[id]
	if !ok {
		c = &cloudChange{lines: lines}
		p.cloud[id] = c
	}
	for _, s := range c.sources {
		if s.String() == source.String() {
			return
		}
	}
	c.sources = append(c.sources, source)
}

func (p *Plan) recordWriteError(source Source, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.writeErrors[source.String()]; !ok {
		p.writeErrors[source.String()] = err
	}
}

func (p *Plan) writeError(source Source) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeErrors[source.String()]
}

// forget drops what source recorded, before its reconcile runs again.
func (p *Plan) forget(source Source) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.writeErrors, source.String())
	for key, bySource := range p.objects {
		delete(bySource, source.String())
		if len(bySource) == 0 {
			delete(p.objects, key)
		}
	}
	for id, c := range p.cloud {
		kept := c.sources[:0]
		for _, s := range c.sources {
			if s.String() != source.String() {
				kept = append(kept, s)
			}
		}
		c.sources = kept
		if len(kept) == 0 {
			delete(p.cloud, id)
		}
	}
}

// created reports whether a source creates an object named name in the API
// group group.
func (p *Plan) created(group, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, bySource := range p.objects {
		if key.Group != group || key.Name != name {
			continue
		}
		for _, w := range bySource {
			if w.created {
				return true
			}
		}
	}
	return false
}

// SetResult records the outcome of the reconcile of source.
func (p *Plan) SetResult(source Source, status Status, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.results[source.String()] = result{source: source, status: status, reason: reason}
}

// Summary counts the changes and the outcomes of a plan.
type Summary struct {
	Create, Update, Reorder, Delete, Cloud int
	OK, Skipped, Pending, Errors           int
}

// Exit codes of a plan, like terraform plan -detailed-exitcode.
const (
	ExitNoChanges = 0
	ExitError     = 1
	ExitChanges   = 2
)

// ExitCode returns ExitError when a reconcile failed or is pending, because
// the plan is then not complete, ExitChanges when the plan changes something
// and ExitNoChanges otherwise.
func (s Summary) ExitCode() int {
	switch {
	case s.Errors > 0 || s.Pending > 0:
		return ExitError
	case s.Create+s.Update+s.Delete+s.Cloud > 0:
		return ExitChanges
	default:
		return ExitNoChanges
	}
}

const (
	labelOrderOnly = "update, order only"
	labelReleased  = "delete, last finalizer removed"
)

// change is what one or more sources would do to an object.
type change struct {
	label   string
	lines   []string
	sources []string
}

// Write writes the plan as a diff, grouped by the clusters of the sources:
// removed lines start with "-" and added lines with "+" in the first column.
// It returns the summary of the plan.
func (p *Plan) Write(w io.Writer) (Summary, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var s Summary
	type entry struct{ group, key, text string }
	var entries []entry

	for key, bySource := range p.objects {
		changes := map[string]*change{}
		clusters := map[string]bool{}
		for _, wr := range bySource {
			c := describe(wr)
			if c == nil {
				continue
			}
			id := c.label + "\n" + strings.Join(c.lines, "\n")
			if existing, ok := changes[id]; ok {
				c = existing
			} else {
				changes[id] = c
			}
			c.sources = append(c.sources, wr.source.String())
			for _, cl := range wr.source.Clusters {
				clusters[cl] = true
			}
		}
		if len(changes) == 0 {
			continue
		}

		var list []*change
		for _, c := range changes {
			sort.Strings(c.sources)
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].sources[0] < list[j].sources[0] })
		// The object is labeled order only when every change is, and a delete
		// wins over the other changes.
		label := labelOrderOnly
		for _, c := range list {
			if c.label != labelOrderOnly {
				label = c.label
				break
			}
		}
		for _, c := range list {
			if symbol(c.label) == "-" {
				label = c.label
			}
		}
		switch symbol(label) {
		case "+":
			s.Create++
		case "-":
			s.Delete++
		default:
			s.Update++
			if label == labelOrderOnly {
				s.Reorder++
			}
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%s %s (%s)\n", symbol(label), key, label)
		for _, c := range list {
			if len(list) > 1 {
				fmt.Fprintf(&b, "  # %s from %s\n", c.label, strings.Join(c.sources, ", "))
			}
			for _, line := range c.lines {
				b.WriteString(line + "\n")
			}
		}
		entries = append(entries, entry{group: strings.Join(sortedKeys(clusters), ", "), key: key.String(), text: b.String()})
	}

	for id, c := range p.cloud {
		clusters := map[string]bool{}
		for _, src := range c.sources {
			for _, cl := range src.Clusters {
				clusters[cl] = true
			}
		}
		s.Cloud++
		entries = append(entries, entry{group: strings.Join(sortedKeys(clusters), ", "), key: "~ " + id, text: id + "\n"})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].group != entries[j].group {
			return entries[i].group < entries[j].group
		}
		return entries[i].key < entries[j].key
	})

	var b strings.Builder
	group := "\x00"
	for _, e := range entries {
		if e.group != group {
			group = e.group
			name := group
			if name == "" {
				name = "(none)"
			}
			fmt.Fprintf(&b, "\n# cluster %s\n", name)
		}
		b.WriteString(e.text)
	}

	var incomplete []result
	for _, r := range p.results {
		switch r.status {
		case StatusOK:
			s.OK++
		case StatusSkipped:
			s.Skipped++
		case StatusPending:
			s.Pending++
			incomplete = append(incomplete, r)
		case StatusError:
			s.Errors++
			incomplete = append(incomplete, r)
		}
	}
	if len(incomplete) > 0 {
		sort.Slice(incomplete, func(i, j int) bool { return incomplete[i].source.String() < incomplete[j].source.String() })
		b.WriteString("\n# Not complete\n")
		for _, r := range incomplete {
			fmt.Fprintf(&b, "! %s %s: %s\n", r.status, r.source, r.reason)
		}
	}

	if len(entries) == 0 && len(incomplete) == 0 {
		b.WriteString("\nNo changes.\n")
	}
	fmt.Fprintf(&b, "\nPlan: %d to create, %d to update (%d only reorder lists), %d to delete, %d AWS/Spot changes. Results: %d ok, %d skipped (paused), %d pending, %d errors.\n",
		s.Create, s.Update, s.Reorder, s.Delete, s.Cloud, s.OK, s.Skipped, s.Pending, s.Errors)

	_, err := io.WriteString(w, strings.TrimPrefix(b.String(), "\n"))
	return s, err
}

func symbol(label string) string {
	switch {
	case strings.HasPrefix(label, "create"):
		return "+"
	case strings.HasPrefix(label, "delete"):
		return "-"
	default:
		return "~"
	}
}

// describe returns the change that w makes, or nil when w changes nothing.
// The delete of an object that is being deleted changes nothing, so the other
// writes to it decide its change.
// Lists are compared and shown with their elements sorted, so that a change
// of their order alone shows as order only.
func describe(w *write) *change {
	switch {
	case w.deleted && !deleting(w.live):
		return &change{label: "delete"}
	case w.created:
		desired := deepCopy(w.desired)
		if status, ok := desired["status"].(map[string]interface{}); ok && len(status) == 0 {
			delete(desired, "status")
		}
		var lines []string
		for _, line := range yamlLines(normalize(desired, true)) {
			lines = append(lines, "+ "+strings.TrimSuffix(line, "\n"))
		}
		return &change{label: "create", lines: lines}
	case reflect.DeepEqual(normalize(w.live, false), normalize(w.desired, false)):
		return nil
	}

	live, desired := normalize(w.live, true), normalize(w.desired, true)
	if reflect.DeepEqual(live, desired) {
		return &change{label: labelOrderOnly}
	}
	label := "update"
	if deleting(w.live) && len(finalizers(w.live)) > 0 && len(finalizers(w.desired)) == 0 {
		// The API server deletes an object that is being deleted when its
		// last finalizer is removed.
		label = labelReleased
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: yamlLines(live), B: yamlLines(desired), Context: 2})
	if err != nil {
		return &change{label: label, lines: []string{fmt.Sprintf("  (cannot show the diff: %v)", err)}}
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		if !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") {
			lines = append(lines, line)
		}
	}
	return &change{label: label, lines: lines}
}

func deleting(obj map[string]interface{}) bool {
	meta, _ := obj["metadata"].(map[string]interface{})
	return meta["deletionTimestamp"] != nil
}

func finalizers(obj map[string]interface{}) []interface{} {
	meta, _ := obj["metadata"].(map[string]interface{})
	f, _ := meta["finalizers"].([]interface{})
	return f
}

func yamlLines(obj map[string]interface{}) []string {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return []string{fmt.Sprintf("(cannot show the object: %v)\n", err)}
	}
	// Every line ends with a newline, which the unified diff needs.
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// normalize returns a copy of obj without empty lists, because an empty list
// and a missing list are the same. With sortLists, the elements of every list
// are sorted, so that objects that differ only in the order of list elements
// are equal.
func normalize(obj map[string]interface{}, sortLists bool) map[string]interface{} {
	var walk func(v interface{}) interface{}
	walk = func(v interface{}) interface{} {
		switch t := v.(type) {
		case map[string]interface{}:
			out := make(map[string]interface{}, len(t))
			for k, e := range t {
				if l, ok := e.([]interface{}); ok && len(l) == 0 {
					continue
				}
				out[k] = walk(e)
			}
			return out
		case []interface{}:
			out := make([]interface{}, len(t))
			for i, e := range t {
				out[i] = walk(e)
			}
			if sortLists {
				sort.Slice(out, func(i, j int) bool { return jsonString(out[i]) < jsonString(out[j]) })
			}
			return out
		default:
			return v
		}
	}
	return walk(obj).(map[string]interface{})
}

func jsonString(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toMap converts obj to the map that the plan compares. It drops the fields
// that the API server sets on every write and, with hideData, replaces the
// values of a Secret with a hash.
func toMap(obj interface{}, hideData bool) (map[string]interface{}, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	delete(m, "apiVersion")
	delete(m, "kind")
	if meta, ok := m["metadata"].(map[string]interface{}); ok {
		for _, f := range []string{"resourceVersion", "managedFields", "generation", "uid", "creationTimestamp", "selfLink"} {
			delete(meta, f)
		}
	}
	if hideData {
		for _, field := range []string{"data", "stringData"} {
			values, _ := m[field].(map[string]interface{})
			for k, v := range values {
				sum := sha256.Sum256([]byte(fmt.Sprint(v)))
				values[k] = "(sensitive, sha256 " + hex.EncodeToString(sum[:])[:12] + ")"
			}
		}
	}
	return m, nil
}

func deepCopy(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	data, _ := json.Marshal(m)
	out := map[string]interface{}{}
	_ = json.Unmarshal(data, &out)
	return out
}
