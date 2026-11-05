package introspect

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func traceTestSource(id string, values map[string]interface{}) ValueSource {
	return ValueSource{ID: id, Kind: "test-file", Name: id + ".yaml", Status: ValueResolved, Values: values}
}

func TestTraceValueSourcesUsesInputOrderAndRecordsEqualAssignments(t *testing.T) {
	first := traceTestSource("first", map[string]interface{}{"replicas": 2})
	first.Order = 9
	second := traceTestSource("second", map[string]interface{}{"replicas": 2})
	second.Order = 1
	unrelated := traceTestSource("unrelated", map[string]interface{}{"image": "example"})
	trace := TraceValueSources([]ValueSource{first, second, unrelated}, ".replicas")
	if len(trace.History) != 2 || trace.History[0].SourceID != "first" || trace.History[1].SourceID != "second" {
		t.Fatalf("history must follow input order and omit unrelated inputs: %#v", trace.History)
	}
	if trace.Value != 2 || !trace.Present || trace.Status != ValueResolved || trace.Changed {
		t.Fatalf("equal assignment should retain the initial known value: %#v", trace)
	}
	if !trace.History[0].Changed || trace.History[1].Changed || !trace.History[0].Shadowed || !trace.History[1].Winner || trace.WinnerSourceID != "second" {
		t.Fatalf("the last explicit scalar assignment must win even when equal: %#v", trace.History)
	}
	if trace.History[0].Order != 9 || trace.History[1].Order != 1 || trace.History[1].Kind != "test-file" || trace.History[1].Name != "second.yaml" {
		t.Fatal("source identity metadata was not retained")
	}
}

func TestTraceValueSourcesChangedComparesFirstAndFinalKnownSnapshots(t *testing.T) {
	sources := []ValueSource{
		traceTestSource("default", map[string]interface{}{"enabled": false}),
		traceTestSource("enable", map[string]interface{}{"enabled": true}),
		traceTestSource("restore", map[string]interface{}{"enabled": false}),
	}
	if trace := TraceValueSources(sources[:2], "enabled"); !trace.Changed {
		t.Fatal("a final value differing from the first resolved source snapshot must be changed")
	}
	trace := TraceValueSources(sources, "enabled")
	if trace.Changed || !trace.History[1].Changed || !trace.History[2].Changed || trace.WinnerSourceID != "restore" {
		t.Fatalf("restoring the first snapshot must clear trace.Changed while preserving history: %#v", trace)
	}
}

func TestTraceValueSourcesChangedIncludesPathsIntroducedAfterBaseline(t *testing.T) {
	defaults := traceTestSource("defaults", map[string]interface{}{"app": map[string]interface{}{"name": "original"}})
	overlay := traceTestSource("operator", map[string]interface{}{"app": map[string]interface{}{"port": 90}})
	unknown := ValueSource{ID: "unknown", Status: ValueUnavailable}
	for _, test := range []struct {
		name    string
		sources []ValueSource
		changed bool
	}{
		{"known absent baseline", []ValueSource{defaults, overlay}, true},
		{"empty known baseline", []ValueSource{traceTestSource("empty", map[string]interface{}{}), overlay}, true},
		{"single source establishes baseline", []ValueSource{overlay}, false},
		{"unknown cannot establish baseline", []ValueSource{unknown, overlay}, false},
		{"first known source establishes absent baseline", []ValueSource{unknown, defaults, overlay}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			trace := TraceValueSources(test.sources, "app.port")
			if trace.Changed != test.changed || !trace.Present || trace.Value != 90 || trace.WinnerSourceID != "operator" {
				t.Fatalf("changed must compare against the first resolved source, including absence: %#v", trace)
			}
		})
	}
}

func TestTraceValueSourcesAncestorReplacementAndRecreation(t *testing.T) {
	for name, replacement := range map[string]interface{}{
		"null": nil, "scalar": false, "list": []interface{}{"replacement"},
	} {
		t.Run(name, func(t *testing.T) {
			sources := []ValueSource{
				traceTestSource("default", map[string]interface{}{"app": map[string]interface{}{"port": 80, "name": "original"}}),
				traceTestSource("replace", map[string]interface{}{"app": replacement}),
				traceTestSource("recreate", map[string]interface{}{"app": map[string]interface{}{"port": 90}}),
			}
			removed := TraceValueSources(sources[:2], ".app.port")
			if removed.Present || removed.Status != ValueMissing || !removed.Changed || removed.WinnerSourceID != "replace" {
				t.Fatalf("ancestor replacement must own the removed leaf: %#v", removed)
			}
			if len(removed.History) != 2 || !removed.History[1].BeforePresent || removed.History[1].AfterPresent || removed.History[1].Before != 80 {
				t.Fatalf("ancestor removal must be visible in history: %#v", removed.History)
			}
			trace := TraceValueSources(sources, ".app.port")
			if len(trace.History) != 3 || trace.History[2].BeforePresent || !trace.History[2].AfterPresent || trace.Value != 90 || trace.WinnerSourceID != "recreate" {
				t.Fatalf("the leaf must be recreated after replacement: %#v", trace)
			}
			object := TraceValueSources(sources, "app")
			if !reflect.DeepEqual(object.Value, map[string]interface{}{"port": 90}) || object.Aggregate || object.WinnerSourceID != "recreate" {
				t.Fatalf("object replacement must not resurrect removed keys: %#v", object)
			}
		})
	}
}

func TestTraceValueSourcesNullIsPresentAndDifferentFromMissing(t *testing.T) {
	source := traceTestSource("null", map[string]interface{}{"nullable": nil, "zero": 0, "disabled": false})
	for _, path := range []string{"nullable", "zero", "disabled"} {
		trace := TraceValueSources([]ValueSource{source}, path)
		if !trace.Present || trace.Status != ValueResolved || !trace.History[0].AfterPresent || !trace.History[0].Changed {
			t.Fatalf("%q must be present even with a zero value: %#v", path, trace)
		}
	}
	missing := TraceValueSources([]ValueSource{source}, "absent")
	if missing.Present || missing.Status != ValueMissing || len(missing.History) != 0 {
		t.Fatalf("an absent key must remain distinct from null: %#v", missing)
	}
}

func TestTraceValueSourcesMapsMergeAndListsReplaceAtomically(t *testing.T) {
	sources := []ValueSource{
		traceTestSource("defaults", map[string]interface{}{"app": map[string]interface{}{"port": 80, "name": "original"}, "items": []interface{}{"one", "two"}}),
		traceTestSource("override", map[string]interface{}{"app": map[string]interface{}{"port": 90}, "items": []interface{}{"three"}}),
	}
	object := TraceValueSources(sources, "app")
	if !reflect.DeepEqual(object.Value, map[string]interface{}{"port": 90, "name": "original"}) || !object.Aggregate || object.WinnerSourceID != "" {
		t.Fatalf("merged object must identify aggregate ownership: %#v", object)
	}
	if len(object.History) != 2 || !object.History[0].Winner || !object.History[1].Winner || object.History[0].Shadowed {
		t.Fatalf("both surviving object contributors must be winners: %#v", object.History)
	}
	name := TraceValueSources(sources, "app.name")
	if len(name.History) != 1 || name.WinnerSourceID != "defaults" || name.Changed {
		t.Fatalf("a sibling patch must not contribute to an untouched leaf: %#v", name)
	}
	items := TraceValueSources(sources, "items")
	if !reflect.DeepEqual(items.Value, []interface{}{"three"}) || items.Aggregate || items.WinnerSourceID != "override" {
		t.Fatalf("lists must replace rather than append or merge: %#v", items)
	}
	index := TraceValueSources(sources, "items.0")
	if index.Present || index.Status != ValueMissing {
		t.Fatalf("dot paths must not infer list element provenance: %#v", index)
	}
}

func TestTraceValueSourcesObjectOwnershipTracksSurvivingLeaves(t *testing.T) {
	sources := []ValueSource{
		traceTestSource("defaults", map[string]interface{}{"app": map[string]interface{}{"port": 80, "name": "original"}}),
		traceTestSource("port", map[string]interface{}{"app": map[string]interface{}{"port": 90}}),
		traceTestSource("name", map[string]interface{}{"app": map[string]interface{}{"name": "new"}}),
		traceTestSource("noop", map[string]interface{}{"app": map[string]interface{}{}}),
	}
	trace := TraceValueSources(sources, "app")
	if !trace.Aggregate || !trace.History[0].Shadowed || !trace.History[1].Winner || !trace.History[2].Winner || !trace.History[3].Shadowed || trace.History[3].Changed {
		t.Fatalf("only sources with surviving leaves should win: %#v", trace.History)
	}
	empty := TraceValueSources([]ValueSource{
		traceTestSource("empty", map[string]interface{}{"app": map[string]interface{}{}}),
		traceTestSource("noop", map[string]interface{}{"app": map[string]interface{}{}}),
	}, "app")
	if empty.WinnerSourceID != "empty" || empty.Changed || empty.Aggregate {
		t.Fatalf("an empty map merge should retain its creator: %#v", empty)
	}
}

func TestTraceValueSourcesUnresolvedInputsCannotClaimEffectiveValues(t *testing.T) {
	for _, status := range []ValueStatus{ValueMissing, ValueDynamic, ValueParseError, ValueUnavailable} {
		t.Run(string(status), func(t *testing.T) {
			unknown := ValueSource{ID: "unknown", Status: status, Values: map[string]interface{}{"port": 999}, Diagnostics: []ValueDiagnostic{{Status: status, Message: "input unresolved"}}}
			known := traceTestSource("known", map[string]interface{}{"port": 80})
			for _, sources := range [][]ValueSource{{known, unknown}, {unknown, known}} {
				trace := TraceValueSources(sources, "port")
				if !trace.Unresolved || trace.Status != status || trace.Value != 80 || !trace.Present || trace.WinnerSourceID != "known" || trace.Changed {
					t.Fatalf("unresolved input must preserve only known static snapshots: %#v", trace)
				}
				if len(trace.History) != 2 {
					t.Fatalf("unresolved input must be visible in ordered history: %#v", trace.History)
				}
				for _, entry := range trace.History {
					if entry.SourceID == "unknown" && (entry.Winner || entry.Shadowed || entry.Changed || len(entry.Diagnostics) != 1) {
						t.Fatalf("unknown input cannot be classified as an applied winner or shadowed: %#v", entry)
					}
				}
			}
			absent := TraceValueSources([]ValueSource{unknown}, "unmentioned")
			if !absent.Unresolved || absent.Status != status || len(absent.History) != 1 {
				t.Fatalf("unknown source may affect even an unmentioned path: %#v", absent)
			}
		})
	}
}

func TestTraceValueSourcesUnresolvedDiagnosticAndLastReason(t *testing.T) {
	diagnostic := traceTestSource("diagnostic", map[string]interface{}{"port": 999})
	diagnostic.Diagnostics = []ValueDiagnostic{{Status: ValueDynamic, Message: "runtime expression"}}
	trace := TraceValueSources([]ValueSource{
		traceTestSource("known", map[string]interface{}{"port": 80}),
		diagnostic,
		{ID: "missing", Status: ValueMissing},
	}, "port")
	if !trace.Unresolved || trace.Status != ValueMissing || trace.Value != 80 || len(trace.History) != 3 || trace.History[1].Status != ValueDynamic {
		t.Fatalf("diagnostics must prevent an effective value claim and preserve the latest reason: %#v", trace)
	}
}

func TestTraceValueSourcesSnapshotsAreIndependent(t *testing.T) {
	first := traceTestSource("first", map[string]interface{}{"app": map[string]interface{}{"items": []interface{}{map[string]interface{}{"name": "original"}}, "port": 80}})
	first.References = []ValueReference{{Scope: "bundle", Path: ".vars.port", Locations: []ValueUsageLocation{{File: "values.yaml"}}}}
	second := traceTestSource("second", map[string]interface{}{"app": map[string]interface{}{"port": 90}})
	trace := TraceValueSources([]ValueSource{first, second}, "app")
	if first.Values["app"].(map[string]interface{})["port"] != 80 {
		t.Fatal("trace merge mutated the source map")
	}
	trace.Value.(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"] = "mutated result"
	trace.History[1].Before.(map[string]interface{})["port"] = 777
	trace.History[0].References[0].Locations[0].File = "mutated reference"
	if first.Values["app"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"] != "original" || first.References[0].Locations[0].File != "values.yaml" {
		t.Fatal("returned snapshots alias the original sources")
	}
	if trace.History[0].After.(map[string]interface{})["port"] != 80 || trace.History[1].After.(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"] != "original" {
		t.Fatal("returned snapshots alias another history entry or final result")
	}
	first.Values["app"].(map[string]interface{})["port"] = 888
	if trace.History[0].After.(map[string]interface{})["port"] != 80 {
		t.Fatal("changing an input after tracing mutated an earlier snapshot")
	}
}

func TestTraceValueSourcesRootAndSerializationOmitValueSnapshots(t *testing.T) {
	sources := []ValueSource{
		traceTestSource("default", map[string]interface{}{"password": "secret-before"}),
		traceTestSource("override", map[string]interface{}{"password": "secret-after"}),
	}
	root := TraceValueSources(sources, ".")
	if !root.Present || !reflect.DeepEqual(root.Value, map[string]interface{}{"password": "secret-after"}) || root.WinnerSourceID != "override" {
		t.Fatalf("root query must trace the complete merged object: %#v", root)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-") || strings.Contains(string(encoded), "password") {
		t.Fatalf("trace metadata leaked a value snapshot: %s", encoded)
	}
	for _, subject := range []struct {
		typeOf reflect.Type
		fields []string
	}{
		{reflect.TypeOf(ValueTrace{}), []string{"Value"}},
		{reflect.TypeOf(ValueContribution{}), []string{"Before", "After"}},
	} {
		for _, name := range subject.fields {
			field, _ := subject.typeOf.FieldByName(name)
			if field.Tag.Get("json") != "-" || field.Tag.Get("yaml") != "-" {
				t.Fatalf("%s.%s must be excluded from JSON and YAML", subject.typeOf.Name(), name)
			}
		}
	}
}

func TestValueTracerCompilationSnapshotsSources(t *testing.T) {
	first := traceTestSource("first", map[string]interface{}{"app": map[string]interface{}{"port": 80, "name": "original"}})
	first.References = []ValueReference{{Scope: "bundle", Path: ".vars.port"}}
	second := traceTestSource("second", map[string]interface{}{"app": map[string]interface{}{"port": 90}})
	sources := []ValueSource{first, second}
	tracer := NewValueTracer(sources)
	want := TraceValueSources(sources, "app")
	first.Values["app"].(map[string]interface{})["name"] = "mutated source"
	second.Values["app"].(map[string]interface{})["port"] = 999
	first.References[0].Path = "mutated reference"
	sources[0].ID = "mutated identity"
	if got := tracer.Trace("app"); !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled traces must be independent of later source mutation: got %#v; want %#v", got, want)
	}
	changed := tracer.Trace("app")
	changed.Value.(map[string]interface{})["name"] = "mutated result"
	changed.History[0].After.(map[string]interface{})["port"] = 777
	changed.History[0].References[0].Path = "mutated returned reference"
	if got := tracer.Trace("app"); !reflect.DeepEqual(got, want) {
		t.Fatalf("one query must not mutate later queries: got %#v; want %#v", got, want)
	}
	if port := tracer.Trace("app.port"); port.Value != 90 || !port.Changed || port.WinnerSourceID != "second" {
		t.Fatalf("compiled leaf query lost merge history: %#v", port)
	}
	if name := tracer.Trace("app.name"); name.Value != "original" || name.Changed || name.WinnerSourceID != "first" {
		t.Fatalf("compiled leaf query lost an unchanged branch: %#v", name)
	}
}

func TestTraceValueSourcesTypedNilMapMatchesMergeReceiver(t *testing.T) {
	var nilMap map[string]interface{}
	trace := TraceValueSources([]ValueSource{
		traceTestSource("nil-map", map[string]interface{}{"app": nilMap}),
		traceTestSource("ignored", map[string]interface{}{"app": map[string]interface{}{"port": 80}}),
	}, "app")
	if !trace.Present || !reflect.DeepEqual(trace.Value, nilMap) || trace.Changed || trace.WinnerSourceID != "nil-map" {
		t.Fatalf("merging into a typed nil map must match Zarf's no-op nil-map receiver: %#v", trace)
	}
}
