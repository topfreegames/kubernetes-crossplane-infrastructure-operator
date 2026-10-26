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

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Source is the object whose reconcile made a write.
type Source struct {
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace,omitempty"`
	Name      string   `json:"name"`
	Clusters  []string `json:"clusters,omitempty"`
}

func (s Source) String() string {
	if s.Namespace == "" {
		return s.Kind + "/" + s.Name
	}
	return s.Kind + "/" + s.Namespace + "/" + s.Name
}

// Action is the kind of change a plan entry makes.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

var actionSymbols = map[Action]string{ActionCreate: "+", ActionUpdate: "~", ActionDelete: "-"}

// Status is the outcome of the reconcile of one object.
type Status string

const (
	StatusOK      Status = "ok"
	StatusSkipped Status = "skipped"
	StatusPending Status = "pending"
	StatusError   Status = "error"
)

// Plan collects the writes that reconciles would make and the outcome of
// each reconcile. A context carries the plan and the source of the reconcile;
// writes made with a context without a plan are only logged.
type Plan struct {
	mu      sync.Mutex
	entries map[objectKey]map[string]*entry
	cloud   map[string]*CloudChange
	results map[string]Result
	// writeErrors has the first failed write of each source.
	writeErrors map[string]error
}

// NewPlan returns an empty plan.
func NewPlan() *Plan {
	return &Plan{
		entries:     map[objectKey]map[string]*entry{},
		cloud:       map[string]*CloudChange{},
		results:     map[string]Result{},
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

type entry struct {
	source  Source
	action  Action
	live    map[string]interface{}
	desired map[string]interface{}
}

// CloudChange is a write to AWS or Spot. Before is empty when the current
// value is not known.
type CloudChange struct {
	Source Source              `json:"-"`
	Kind   string              `json:"kind"`
	ID     string              `json:"id"`
	Call   string              `json:"call"`
	Before map[string][]string `json:"before,omitempty"`
	After  map[string][]string `json:"after"`

	sources []Source
}

// Result is the outcome of the reconcile of one object.
type Result struct {
	Source Source `json:"source"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
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

func keyFor(gvk schema.GroupVersionKind, obj client.Object) objectKey {
	return objectKey{Group: gvk.Group, Kind: gvk.Kind, Namespace: obj.GetNamespace(), Name: obj.GetName()}
}

// entryFor returns the entry of key for source, starting from live.
func (p *Plan) entryFor(key objectKey, source Source, live map[string]interface{}) *entry {
	bySource, ok := p.entries[key]
	if !ok {
		bySource = map[string]*entry{}
		p.entries[key] = bySource
	}
	e, ok := bySource[source.String()]
	if !ok {
		e = &entry{source: source, action: ActionUpdate, live: live, desired: deepCopyMap(live)}
		bySource[source.String()] = e
	}
	return e
}

func (p *Plan) recordCreate(source Source, key objectKey, obj map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entryFor(key, source, nil)
	e.action = ActionCreate
	e.desired = obj
}

// recordUpdate records a write of the main resource. The status of obj is
// ignored, like the API server does for resources with a status subresource.
func (p *Plan) recordUpdate(source Source, key objectKey, live, obj map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entryFor(key, source, live)
	if e.action == ActionDelete {
		return
	}
	desired := deepCopyMap(obj)
	if status, ok := e.desired["status"]; ok {
		desired["status"] = status
	} else {
		delete(desired, "status")
	}
	e.desired = desired
}

func (p *Plan) recordStatusUpdate(source Source, key objectKey, live, obj map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entryFor(key, source, live)
	if e.action == ActionDelete {
		return
	}
	if e.desired == nil {
		e.desired = map[string]interface{}{}
	}
	if status, ok := obj["status"]; ok {
		e.desired["status"] = status
	} else {
		delete(e.desired, "status")
	}
}

func (p *Plan) recordDelete(source Source, key objectKey, live map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entryFor(key, source, live)
	e.action = ActionDelete
	e.desired = nil
}

// RecordCloudChange records a write to AWS or Spot. The same change made by
// several reconciles is kept once.
func (p *Plan) RecordCloudChange(c CloudChange) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := cloudChangeID(c)
	if existing, ok := p.cloud[id]; ok {
		for _, s := range existing.sources {
			if s.String() == c.Source.String() {
				return
			}
		}
		existing.sources = append(existing.sources, c.Source)
		return
	}
	c.sources = []Source{c.Source}
	p.cloud[id] = &c
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

// unchanged reports whether every value of c is the same before and after.
// It is false when the values before are not known.
func (c *CloudChange) unchanged() bool {
	if len(c.Before) == 0 {
		return false
	}
	for field, after := range c.After {
		before, ok := c.Before[field]
		if !ok || !reflect.DeepEqual(before, after) {
			return false
		}
	}
	return true
}

// cloudChangeID identifies a change by its kind, ID, call and values.
func cloudChangeID(c CloudChange) string {
	data, _ := json.Marshal(c)
	return string(data)
}

// forget drops what source recorded, before its reconcile runs again.
func (p *Plan) forget(source Source) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.writeErrors, source.String())
	for key, bySource := range p.entries {
		delete(bySource, source.String())
		if len(bySource) == 0 {
			delete(p.entries, key)
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

// createdName reports whether a source created an object named name in the
// API group group.
func (p *Plan) createdName(group, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, bySource := range p.entries {
		if key.Group != group || key.Name != name {
			continue
		}
		for _, e := range bySource {
			if e.action == ActionCreate {
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
	p.results[source.String()] = Result{Source: source, Status: status, Reason: reason}
}

// Change is one object that the plan changes.
type Change struct {
	Clusters  []string  `json:"clusters"`
	Group     string    `json:"group,omitempty"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	Action    Action    `json:"action"`
	Variants  []Variant `json:"variants"`
}

func (c Change) orderOnly() bool {
	for _, v := range c.Variants {
		if !v.OrderOnly {
			return false
		}
	}
	return true
}

// Variant is a change that one or more sources would make. An object has
// more than one variant when reconciles disagree on it.
type Variant struct {
	Action  Action   `json:"action"`
	Sources []string `json:"sources"`
	Diff    []string `json:"diff"`
	// Released is true when the update removes the last finalizer of an
	// object that is being deleted, so the API server deletes the object.
	Released bool `json:"released,omitempty"`
	// OrderOnly is true when the update only changes the order of list
	// elements.
	OrderOnly bool `json:"orderOnly,omitempty"`
}

// CloudChangeSummary is a CloudChange with the clusters and sources that make it.
type CloudChangeSummary struct {
	CloudChange
	Clusters []string `json:"clusters"`
	Sources  []string `json:"sources"`
}

// Summary counts the changes and outcomes of a plan.
type Summary struct {
	Create int `json:"create"`
	Update int `json:"update"`
	// Reorder is the number of updates that only change the order of list
	// elements. They are counted in Update.
	Reorder int `json:"reorder"`
	Delete  int `json:"delete"`
	Cloud   int `json:"cloud"`
	OK      int `json:"ok"`
	Skipped int `json:"skipped"`
	Pending int `json:"pending"`
	Errors  int `json:"errors"`
}

// Report is the result of a plan.
type Report struct {
	Summary Summary              `json:"summary"`
	Changes []Change             `json:"changes"`
	Cloud   []CloudChangeSummary `json:"cloud"`
	Results []Result             `json:"results"`
}

// HasChanges reports whether the plan changes anything.
func (r *Report) HasChanges() bool {
	return len(r.Changes) > 0 || len(r.Cloud) > 0
}

// HasErrors reports whether a reconcile failed.
func (r *Report) HasErrors() bool {
	return r.Summary.Errors > 0
}

// Report computes the changes of the plan, without the writes that change
// nothing, in a stable order.
func (p *Plan) Report() *Report {
	p.mu.Lock()
	defer p.mu.Unlock()

	r := &Report{Changes: []Change{}, Cloud: []CloudChangeSummary{}, Results: []Result{}}
	for key, bySource := range p.entries {
		variants := map[string]*Variant{}
		clusters := map[string]bool{}
		for _, e := range bySource {
			diff := diffObjects(e.live, e.desired, e.action)
			if len(diff) == 0 && e.action == ActionUpdate || e.action == ActionDelete && deleting(e.live) {
				continue
			}
			action, released := e.action, false
			if action == ActionUpdate && releases(e.live, e.desired) {
				action, released = ActionDelete, true
			}
			id := string(action) + "\x00" + strings.Join(diff, "\n")
			v, ok := variants[id]
			if !ok {
				v = &Variant{Action: action, Diff: diff, Released: released, OrderOnly: action == ActionUpdate && orderOnly(diff)}
				variants[id] = v
			}
			v.Sources = append(v.Sources, e.source.String())
			for _, c := range e.source.Clusters {
				clusters[c] = true
			}
		}
		if len(variants) == 0 {
			continue
		}
		c := Change{Group: key.Group, Kind: key.Kind, Namespace: key.Namespace, Name: key.Name, Clusters: sortedKeys(clusters)}
		for _, v := range variants {
			sort.Strings(v.Sources)
			c.Variants = append(c.Variants, *v)
		}
		sort.Slice(c.Variants, func(i, j int) bool { return c.Variants[i].Sources[0] < c.Variants[j].Sources[0] })
		c.Action = c.Variants[0].Action
		for _, v := range c.Variants {
			if v.Action == ActionDelete {
				c.Action = ActionDelete
			}
		}
		switch c.Action {
		case ActionCreate:
			r.Summary.Create++
		case ActionUpdate:
			r.Summary.Update++
			if c.orderOnly() {
				r.Summary.Reorder++
			}
		case ActionDelete:
			r.Summary.Delete++
		}
		r.Changes = append(r.Changes, c)
	}
	sort.Slice(r.Changes, func(i, j int) bool {
		a, b := r.Changes[i], r.Changes[j]
		if ca, cb := strings.Join(a.Clusters, ","), strings.Join(b.Clusters, ","); ca != cb {
			return ca < cb
		}
		return objectKey{a.Group, a.Kind, a.Namespace, a.Name}.String() < objectKey{b.Group, b.Kind, b.Namespace, b.Name}.String()
	})

	for _, c := range p.cloud {
		if c.unchanged() {
			continue
		}
		s := CloudChangeSummary{CloudChange: *c}
		clusters := map[string]bool{}
		for _, src := range c.sources {
			s.Sources = append(s.Sources, src.String())
			for _, cl := range src.Clusters {
				clusters[cl] = true
			}
		}
		sort.Strings(s.Sources)
		s.Clusters = sortedKeys(clusters)
		r.Cloud = append(r.Cloud, s)
	}
	sort.Slice(r.Cloud, func(i, j int) bool {
		a, b := r.Cloud[i], r.Cloud[j]
		if ca, cb := strings.Join(a.Clusters, ","), strings.Join(b.Clusters, ","); ca != cb {
			return ca < cb
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Call != b.Call {
			return a.Call < b.Call
		}
		return cloudChangeID(a.CloudChange) < cloudChangeID(b.CloudChange)
	})
	r.Summary.Cloud = len(r.Cloud)

	for _, res := range p.results {
		r.Results = append(r.Results, res)
		switch res.Status {
		case StatusOK:
			r.Summary.OK++
		case StatusSkipped:
			r.Summary.Skipped++
		case StatusPending:
			r.Summary.Pending++
		case StatusError:
			r.Summary.Errors++
		}
	}
	sort.Slice(r.Results, func(i, j int) bool { return r.Results[i].Source.String() < r.Results[j].Source.String() })
	return r
}

// WriteText writes the report as a diff: removed values start with "-" and
// added values with "+" in the first column.
func (r *Report) WriteText(w io.Writer) error {
	var b strings.Builder
	group := "\x00"
	header := func(clusters []string) {
		g := strings.Join(clusters, ", ")
		if g == group {
			return
		}
		group = g
		if g == "" {
			g = "(none)"
		}
		fmt.Fprintf(&b, "\n# cluster %s\n", g)
	}

	for _, c := range r.Changes {
		header(c.Clusters)
		key := objectKey{c.Group, c.Kind, c.Namespace, c.Name}
		label := string(c.Action)
		if c.Action == ActionUpdate && c.orderOnly() {
			label += ", order only"
		}
		fmt.Fprintf(&b, "%s %s (%s)\n", actionSymbols[c.Action], key, label)
		for _, v := range c.Variants {
			if len(c.Variants) > 1 {
				fmt.Fprintf(&b, "  # %s from %s\n", v.Action, strings.Join(v.Sources, ", "))
			}
			if v.Released {
				b.WriteString("  # removes the last finalizer of an object that is being deleted, so the object is deleted\n")
			}
			for _, line := range v.Diff {
				fmt.Fprintf(&b, "%s\n", line)
			}
		}
	}

	if len(r.Cloud) > 0 {
		group = "\x00"
		b.WriteString("\n# AWS and Spot\n")
		for _, c := range r.Cloud {
			header(c.Clusters)
			fmt.Fprintf(&b, "~ %s %s (%s)\n", c.Kind, c.ID, c.Call)
			for _, field := range sortedKeys(c.After) {
				if before, ok := c.Before[field]; ok {
					if reflect.DeepEqual(before, c.After[field]) {
						continue
					}
					fmt.Fprintf(&b, "-     %s: %s\n", field, formatList(before))
				}
				fmt.Fprintf(&b, "+     %s: %s\n", field, formatList(c.After[field]))
			}
		}
	}

	var problems []Result
	for _, res := range r.Results {
		if res.Status == StatusError || res.Status == StatusPending {
			problems = append(problems, res)
		}
	}
	if len(problems) > 0 {
		b.WriteString("\n# Not complete\n")
		for _, res := range problems {
			fmt.Fprintf(&b, "! %s %s: %s\n", res.Status, res.Source, res.Reason)
		}
	}

	if !r.HasChanges() {
		b.WriteString("\nNo changes.\n")
	}
	s := r.Summary
	fmt.Fprintf(&b, "\nPlan: %d to create, %d to update (%d only reorder lists), %d to delete, %d AWS/Spot changes. Reconciled %d objects: %d skipped (paused), %d pending, %d errors.\n",
		s.Create, s.Update, s.Reorder, s.Delete, s.Cloud, s.OK+s.Skipped+s.Pending+s.Errors, s.Skipped, s.Pending, s.Errors)

	_, err := io.WriteString(w, strings.TrimPrefix(b.String(), "\n"))
	return err
}

func formatList(values []string) string {
	return "[" + strings.Join(values, ", ") + "]"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toMap converts obj to the map that the plan compares. It drops the fields
// that the API server sets on every write and hides Secret values.
func toMap(obj interface{}) (map[string]interface{}, error) {
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
		if len(meta) == 0 {
			delete(m, "metadata")
		}
	}
	return m, nil
}

// hideSecretData replaces the values of a Secret with a hash, so that a change
// shows without the value.
func hideSecretData(m map[string]interface{}) {
	for _, field := range []string{"data", "stringData"} {
		data, ok := m[field].(map[string]interface{})
		if !ok {
			continue
		}
		for k, v := range data {
			sum := sha256.Sum256([]byte(fmt.Sprint(v)))
			data[k] = "(sensitive, sha256 " + hex.EncodeToString(sum[:])[:12] + ")"
		}
	}
}

func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyMap(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}

// diffObjects returns the diff lines from live to desired. A delete has no
// field lines.
func diffObjects(live, desired map[string]interface{}, action Action) []string {
	if action == ActionDelete {
		return []string{}
	}
	if action == ActionCreate {
		live = nil
		desired = deepCopyMap(desired)
		if status, ok := desired["status"].(map[string]interface{}); ok && len(status) == 0 {
			delete(desired, "status")
		}
	}
	var lines []string
	diffValue("", live, desired, &lines)
	if lines == nil {
		lines = []string{}
	}
	return lines
}

// diffValue appends the differences between a and b under path. The order of
// list elements is ignored: list elements are matched by their "key" or
// "name" field, or else by value.
func diffValue(path string, a, b interface{}, lines *[]string) {
	am, aIsMap := a.(map[string]interface{})
	bm, bIsMap := b.(map[string]interface{})
	if (aIsMap || a == nil) && (bIsMap || b == nil) && (aIsMap || bIsMap) {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			diffValue(joinPath(path, k), am[k], bm[k], lines)
		}
		return
	}

	al, aIsList := a.([]interface{})
	bl, bIsList := b.([]interface{})
	if (aIsList || a == nil) && (bIsList || b == nil) && (aIsList || bIsList) {
		diffList(path, al, bl, lines)
		return
	}

	if reflect.DeepEqual(a, b) {
		return
	}
	if a != nil {
		*lines = append(*lines, "-     "+path+": "+formatValue(a))
	}
	if b != nil {
		*lines = append(*lines, "+     "+path+": "+formatValue(b))
	}
}

func diffList(path string, a, b []interface{}, lines *[]string) {
	// An empty list and a missing list are the same.
	if len(a) == 0 && len(b) == 0 {
		return
	}
	if field := listKey(a, b); field != "" {
		am, bm := map[string]interface{}{}, map[string]interface{}{}
		for _, e := range a {
			am[e.(map[string]interface{})[field].(string)] = e
		}
		for _, e := range b {
			bm[e.(map[string]interface{})[field].(string)] = e
		}
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		before := len(*lines)
		for _, k := range sortedKeys(keys) {
			diffValue(fmt.Sprintf("%s[%s=%s]", path, field, k), am[k], bm[k], lines)
		}
		if len(*lines) == before && !reflect.DeepEqual(a, b) {
			*lines = append(*lines, orderLine(path))
		}
		return
	}

	// Elements without a key are compared by value, ignoring the order of
	// the lists inside them, so that only a change of order inside an
	// element shows as an order change.
	values := map[string]string{}
	count := func(l []interface{}) map[string]int {
		c := map[string]int{}
		for _, e := range l {
			k := formatValue(canonical(e))
			if _, ok := values[k]; !ok {
				values[k] = formatValue(e)
			}
			c[k]++
		}
		return c
	}
	ac, bc := count(a), count(b)
	if reflect.DeepEqual(ac, bc) {
		if !reflect.DeepEqual(a, b) {
			*lines = append(*lines, orderLine(path))
		}
		return
	}
	for _, k := range sortedKeys(ac) {
		for i := bc[k]; i < ac[k]; i++ {
			*lines = append(*lines, "-     "+path+"[]: "+values[k])
		}
	}
	for _, k := range sortedKeys(bc) {
		for i := ac[k]; i < bc[k]; i++ {
			*lines = append(*lines, "+     "+path+"[]: "+values[k])
		}
	}
}

// canonical returns v with the elements of every list in it sorted, so that
// two values that differ only in the order of list elements are equal.
func canonical(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, e := range t {
			out[k] = canonical(e)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = canonical(e)
		}
		sort.Slice(out, func(i, j int) bool { return formatValue(out[i]) < formatValue(out[j]) })
		return out
	default:
		return v
	}
}

// orderPrefix starts the line of a list whose elements stay the same but
// change order.
const orderPrefix = "~     "

func orderLine(path string) string {
	return orderPrefix + path + ": the order of the elements changes"
}

func orderOnly(diff []string) bool {
	for _, line := range diff {
		if !strings.HasPrefix(line, orderPrefix) {
			return false
		}
	}
	return len(diff) > 0
}

// deleting reports whether live is being deleted already.
func deleting(live map[string]interface{}) bool {
	meta, _ := live["metadata"].(map[string]interface{})
	return meta["deletionTimestamp"] != nil
}

// releases reports whether desired removes the last finalizer of live, an
// object that is being deleted. The API server then deletes the object.
func releases(live, desired map[string]interface{}) bool {
	if !deleting(live) {
		return false
	}
	liveMeta, _ := live["metadata"].(map[string]interface{})
	desiredMeta, _ := desired["metadata"].(map[string]interface{})
	liveFinalizers, _ := liveMeta["finalizers"].([]interface{})
	desiredFinalizers, _ := desiredMeta["finalizers"].([]interface{})
	return len(liveFinalizers) > 0 && len(desiredFinalizers) == 0
}

// listKey returns the field that identifies the elements of a and b, or ""
// when there is none.
func listKey(a, b []interface{}) string {
	if len(a)+len(b) == 0 {
		return ""
	}
	for _, field := range []string{"key", "name"} {
		ok := true
		for _, l := range [][]interface{}{a, b} {
			seen := map[string]bool{}
			for _, e := range l {
				m, isMap := e.(map[string]interface{})
				if !isMap {
					ok = false
					break
				}
				v, isString := m[field].(string)
				if !isString || seen[v] {
					ok = false
					break
				}
				seen[v] = true
			}
		}
		if ok {
			return field
		}
	}
	return ""
}

func joinPath(path, key string) string {
	if strings.ContainsAny(key, ".[]") {
		key = "[" + key + "]"
		return path + key
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

func formatValue(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}
