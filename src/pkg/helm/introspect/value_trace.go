package introspect

import (
	"reflect"
	"strings"
)

// ValueTrace explains the ordered static merge for one dot-separated value path.
// Value is the known snapshot, not a deployment result; Unresolved means that an
// uninspected input may affect it. The root path is "." (or an empty string).
type ValueTrace struct {
	Path           string              `json:"path" yaml:"path"`
	History        []ValueContribution `json:"history,omitempty" yaml:"history,omitempty"`
	Status         ValueStatus         `json:"status" yaml:"status"`
	Value          interface{}         `json:"-" yaml:"-"`
	Present        bool                `json:"present" yaml:"present"`
	Changed        bool                `json:"changed" yaml:"changed"`
	Unresolved     bool                `json:"unresolved" yaml:"unresolved"`
	Aggregate      bool                `json:"aggregate" yaml:"aggregate"`
	WinnerSourceID string              `json:"winnerSourceId,omitempty" yaml:"winnerSourceId,omitempty"`
}

// ValueContribution records a source that explicitly supplies a path, replaces
// an ancestor, or cannot be inspected. Before and After are independent known
// snapshots. Changed compares those snapshots, including presence versus null.
// Winner and Shadowed describe the known static merge only. An object can have
// multiple winners when its surviving children come from different sources.
type ValueContribution struct {
	SourceID      string            `json:"sourceId" yaml:"sourceId"`
	Kind          string            `json:"kind" yaml:"kind"`
	Name          string            `json:"name" yaml:"name"`
	Order         int               `json:"order" yaml:"order"`
	Status        ValueStatus       `json:"status" yaml:"status"`
	Diagnostics   []ValueDiagnostic `json:"diagnostics,omitempty" yaml:"diagnostics,omitempty"`
	References    []ValueReference  `json:"references,omitempty" yaml:"references,omitempty"`
	Before        interface{}       `json:"-" yaml:"-"`
	After         interface{}       `json:"-" yaml:"-"`
	BeforePresent bool              `json:"beforePresent" yaml:"beforePresent"`
	AfterPresent  bool              `json:"afterPresent" yaml:"afterPresent"`
	Changed       bool              `json:"changed" yaml:"changed"`
	Winner        bool              `json:"winner" yaml:"winner"`
	Shadowed      bool              `json:"shadowed" yaml:"shadowed"`
}

// ValueTracer compiles immutable input and merge trees for repeated path queries.
// Unchanged branches are shared internally; returned snapshots are independent.
type ValueTracer struct {
	states []valueTraceState
	final  *valueTraceNode
}

type valueTraceState struct {
	source ValueSource
	status ValueStatus
	input  *valueTraceNode
	before *valueTraceNode
	after  *valueTraceNode
}

// NewValueTracer uses source slice order, matching the ordered input to Zarf's
// DeepMerge. Order is display metadata, never used to sort. Only two
// map[string]interface{} values merge recursively; every other input, including
// a list or null, replaces the previous value atomically. Source snapshots are
// copied during compilation so callers can reuse or change their inputs safely.
func NewValueTracer(sources []ValueSource) *ValueTracer {
	tracer := &ValueTracer{final: &valueTraceNode{isMap: true, children: map[string]*valueTraceNode{}, owner: -1}}
	for sourceIndex, source := range sources {
		state := valueTraceState{status: traceSourceStatus(source), before: tracer.final}
		if state.status == ValueResolved {
			state.input = newTraceNode(source.Values, sourceIndex)
			tracer.final = mergeTraceNodes(tracer.final, state.input)
		}
		state.after = tracer.final
		state.source = source
		state.source.Values = nil
		state.source.Diagnostics = append([]ValueDiagnostic(nil), source.Diagnostics...)
		state.source.References = cloneTraceReferences(source.References)
		tracer.states = append(tracer.states, state)
	}
	return tracer
}

// TraceValueSources compiles sources and traces one path. Use NewValueTracer
// when querying many paths from the same inputs.
func TraceValueSources(sources []ValueSource, path string) ValueTrace {
	return NewValueTracer(sources).Trace(path)
}

// Trace explains one dot-separated path using the compiled source snapshots.
// Leaf queries walk only the requested path for each source; object queries copy
// the selected subtree for every contributing source's Before and After values.
//
// Trace.Changed compares the final known snapshot with the snapshot after the
// first resolved source, including an absent path. A later overlay introducing
// that path is changed; a later restoration to its initial value is unchanged.
// A sole resolved source establishes the baseline and is therefore unchanged.
// Sources with unresolved status or diagnostics remain in History without being
// applied or establishing the baseline. Any such source conservatively marks
// the trace unresolved, even if a later known source supplies the requested path.
func (tracer *ValueTracer) Trace(path string) ValueTrace {
	trace := ValueTrace{Path: path, Status: ValueResolved}
	parts := tracePathParts(path)
	historyBySource := make(map[int]int)
	firstKnown, lastKnown := -1, -1

	for sourceIndex, state := range tracer.states {
		source := state.source
		resolved := state.status == ValueResolved
		if resolved && firstKnown < 0 {
			firstKnown = sourceIndex
		}
		contributes := traceSourceAffects(state.input, parts)
		if resolved && !contributes {
			continue
		}

		before, beforePresent := traceNodeSnapshot(state.before, parts)
		entry := ValueContribution{
			SourceID: source.ID, Kind: source.Kind, Name: source.Name,
			Order: source.Order, Status: state.status,
			Diagnostics: append([]ValueDiagnostic(nil), source.Diagnostics...),
			References:  cloneTraceReferences(source.References),
			Before:      before, BeforePresent: beforePresent,
		}
		if resolved {
			lastKnown = len(trace.History)
			historyBySource[sourceIndex] = len(trace.History)
		} else {
			trace.Unresolved = true
			trace.Status = state.status
		}
		entry.After, entry.AfterPresent = traceNodeSnapshot(state.after, parts)
		entry.Changed = entry.BeforePresent != entry.AfterPresent || !reflect.DeepEqual(entry.Before, entry.After)
		trace.History = append(trace.History, entry)
	}

	trace.Value, trace.Present = traceNodeSnapshot(tracer.final, parts)
	if firstKnown >= 0 {
		baseline, baselinePresent := traceNodeSnapshot(tracer.states[firstKnown].after, parts)
		trace.Changed = baselinePresent != trace.Present || !reflect.DeepEqual(baseline, trace.Value)
	}
	if !trace.Unresolved && !trace.Present {
		trace.Status = ValueMissing
	}

	winners := make(map[int]bool)
	if node, found := traceNodeAt(tracer.final, parts); found {
		collectTraceOwners(node, winners)
	} else if lastKnown >= 0 {
		// The last ancestor replacement owns a known absence just as a direct
		// assignment owns a value. An explicit null remains present instead.
		trace.History[lastKnown].Winner = true
	}
	for sourceIndex := range winners {
		if historyIndex, found := historyBySource[sourceIndex]; found {
			trace.History[historyIndex].Winner = true
		}
	}
	winnerCount := 0
	for i := range trace.History {
		entry := &trace.History[i]
		if entry.Winner {
			winnerCount++
			trace.WinnerSourceID = entry.SourceID
		} else if entry.Status == ValueResolved {
			entry.Shadowed = true
		}
	}
	trace.Aggregate = winnerCount > 1
	if trace.Aggregate {
		trace.WinnerSourceID = ""
	}
	return trace
}

func tracePathParts(path string) []string {
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil
	}
	return strings.Split(path, ".")
}

func traceSourceStatus(source ValueSource) ValueStatus {
	status := source.Status
	if status == "" {
		status = ValueResolved
	}
	for _, diagnostic := range source.Diagnostics {
		if diagnostic.Status != "" && diagnostic.Status != ValueResolved {
			status = diagnostic.Status
		}
	}
	return status
}

func traceSourceAffects(input *valueTraceNode, parts []string) bool {
	current := input
	for _, part := range parts {
		if current == nil {
			return false
		}
		if !current.isMap {
			// A list, scalar, or null at an ancestor replaces all descendants.
			return true
		}
		var found bool
		current, found = current.children[part]
		if !found {
			return false
		}
	}
	return true
}

// A map owns its empty shape; nonempty maps derive ownership from their leaves.
// This avoids claiming that the last object patch replaced untouched children.
type valueTraceNode struct {
	value    interface{}
	isMap    bool
	children map[string]*valueTraceNode
	owner    int
}

func newTraceNode(incoming interface{}, owner int) *valueTraceNode {
	if mapping, isMap := incoming.(map[string]interface{}); isMap {
		node := &valueTraceNode{isMap: true, owner: owner}
		if mapping != nil {
			node.children = make(map[string]*valueTraceNode, len(mapping))
		}
		for key, value := range mapping {
			node.children[key] = newTraceNode(value, owner)
		}
		return node
	}
	return &valueTraceNode{value: cloneTraceValue(incoming), owner: owner}
}

func mergeTraceNodes(destination, incoming *valueTraceNode) *valueTraceNode {
	if destination == nil || !destination.isMap || !incoming.isMap {
		return incoming
	}
	// Zarf's nil-map receiver is a no-op, and a nil/empty source has no keys.
	if destination.children == nil || len(incoming.children) == 0 {
		if destination.owner < 0 && incoming.children != nil {
			return &valueTraceNode{isMap: true, children: destination.children, owner: incoming.owner}
		}
		return destination
	}
	merged := &valueTraceNode{isMap: true, children: make(map[string]*valueTraceNode, len(destination.children)+len(incoming.children)), owner: destination.owner}
	if merged.owner < 0 {
		merged.owner = incoming.owner
	}
	for key, child := range destination.children {
		merged.children[key] = child
	}
	for key, child := range incoming.children {
		merged.children[key] = mergeTraceNodes(destination.children[key], child)
	}
	return merged
}

func traceNodeAt(root *valueTraceNode, parts []string) (*valueTraceNode, bool) {
	node := root
	for _, part := range parts {
		if node == nil || node.children == nil {
			return nil, false
		}
		var found bool
		node, found = node.children[part]
		if !found {
			return nil, false
		}
	}
	return node, node != nil
}

func traceNodeSnapshot(root *valueTraceNode, parts []string) (interface{}, bool) {
	node, found := traceNodeAt(root, parts)
	if !found {
		return nil, false
	}
	return snapshotTraceNode(node), true
}

func snapshotTraceNode(node *valueTraceNode) interface{} {
	if !node.isMap {
		return cloneTraceValue(node.value)
	}
	if node.children == nil {
		return map[string]interface{}(nil)
	}
	result := make(map[string]interface{}, len(node.children))
	for key, child := range node.children {
		result[key] = snapshotTraceNode(child)
	}
	return result
}

func collectTraceOwners(node *valueTraceNode, owners map[int]bool) {
	if len(node.children) == 0 {
		if node.owner >= 0 {
			owners[node.owner] = true
		}
		return
	}
	for _, child := range node.children {
		collectTraceOwners(child, owners)
	}
}

func cloneTraceValue(value interface{}) interface{} {
	switch value := value.(type) {
	case map[string]interface{}:
		if value == nil {
			return map[string]interface{}(nil)
		}
		cloned := make(map[string]interface{}, len(value))
		for key, child := range value {
			cloned[key] = cloneTraceValue(child)
		}
		return cloned
	case []interface{}:
		if value == nil {
			return []interface{}(nil)
		}
		cloned := make([]interface{}, len(value))
		for i, child := range value {
			cloned[i] = cloneTraceValue(child)
		}
		return cloned
	default:
		return value
	}
}

func cloneTraceReferences(references []ValueReference) []ValueReference {
	cloned := append([]ValueReference(nil), references...)
	for i := range cloned {
		cloned[i].Locations = append([]ValueUsageLocation(nil), cloned[i].Locations...)
	}
	return cloned
}
