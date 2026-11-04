package introspect

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/zarf-dev/zarf/src/pkg/value"
)

// inspectNextValuesTemplate evaluates only literal bundle .vars lookups. Control
// flow, function calls and computed keys remain evidence of an unresolved input.
// It never executes Zarf package templates or actions.
func inspectNextValuesTemplate(data []byte, name string, vars map[string]interface{}) ([]byte, ValueSource) {
	source := ValueSource{Kind: "bundle-values", Name: name, Status: ValueResolved}
	if !bytes.Contains(data, []byte("{{")) {
		return data, source
	}
	tmpl, err := template.New(name).Funcs(helmFuncMap()).Option("missingkey=error").Parse(string(data))
	if err != nil {
		source.Status = ValueParseError
		message := "Bundle values template could not be parsed."
		if strings.Contains(err.Error(), "function") && strings.Contains(err.Error(), "not defined") {
			source.Status = ValueDynamic
			message = "Bundle values template uses a function outside static inspection."
		}
		source.Diagnostics = []ValueDiagnostic{{Status: source.Status, Message: message}}
		return nil, source
	}
	computed := false
	seen := map[string]int{}
	addRef := func(path string, pos parse.Pos, direct bool) {
		status := ValueResolved
		if _, err := value.Values(vars).Extract(value.Path(path)); err != nil {
			status = ValueMissing
		}
		offset := int(pos)
		if offset > len(data) {
			offset = len(data)
		}
		line := bytes.Count(data[:offset], []byte("\n")) + 1
		column := offset - bytes.LastIndexByte(data[:offset], '\n')
		location := ValueUsageLocation{File: name, Line: line, Column: column, Kind: "bundleVariable"}
		if existing, ok := seen[path]; ok {
			source.References[existing].Computed = source.References[existing].Computed || !direct
			source.References[existing].Locations = append(source.References[existing].Locations, location)
			return
		}
		seen[path] = len(source.References)
		source.References = append(source.References, ValueReference{Scope: "bundle-vars", Path: path,
			Status: status, Computed: !direct, Locations: []ValueUsageLocation{location}})
	}
	var walk func(parse.Node, bool)
	walk = func(node parse.Node, direct bool) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *parse.ListNode:
			if n != nil {
				for _, child := range n.Nodes {
					walk(child, false)
				}
			}
		case *parse.ActionNode:
			path, ok := nextLiteralVariable(n.Pipe)
			if ok {
				addRef(path, n.Pos, true)
			} else {
				computed = true
				walk(n.Pipe, false)
			}
		case *parse.PipeNode:
			if n == nil {
				return
			}
			for _, command := range n.Cmds {
				walk(command, direct)
			}
		case *parse.CommandNode:
			for _, arg := range n.Args {
				walk(arg, direct)
			}
		case *parse.FieldNode:
			if len(n.Ident) > 1 && n.Ident[0] == "vars" {
				addRef("."+strings.Join(n.Ident[1:], "."), n.Pos, direct)
			}
		case *parse.ChainNode:
			walk(n.Node, false)
		case *parse.IfNode:
			computed = true
			walk(n.Pipe, false)
			walk(n.List, false)
			walk(n.ElseList, false)
		case *parse.RangeNode:
			computed = true
			walk(n.Pipe, false)
			walk(n.List, false)
			walk(n.ElseList, false)
		case *parse.WithNode:
			computed = true
			walk(n.Pipe, false)
			walk(n.List, false)
			walk(n.ElseList, false)
		case *parse.TemplateNode:
			computed = true
			walk(n.Pipe, false)
		}
	}
	for _, associated := range tmpl.Templates() {
		if associated.Name() != name {
			computed = true
		}
		if associated.Tree != nil {
			walk(associated.Tree.Root, false)
		}
	}
	if computed {
		source.Status = ValueDynamic
		source.Diagnostics = []ValueDiagnostic{{Status: source.Status, Message: "Computed bundle template is not evaluated; only literal .vars lookups are resolved."}}
		return nil, source
	}
	for _, reference := range source.References {
		if reference.Status == ValueMissing {
			source.Status = ValueMissing
			source.Diagnostics = append(source.Diagnostics, ValueDiagnostic{Status: ValueMissing, Path: reference.Path, Message: "Bundle variable is absent from inspected defaults and configuration."})
		}
	}
	if source.Status != ValueResolved {
		return nil, source
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, map[string]interface{}{"vars": vars}); err != nil {
		source.Status = ValueUnavailable
		source.Diagnostics = []ValueDiagnostic{{Status: source.Status, Message: "Literal bundle variables could not be rendered."}}
		return nil, source
	}
	return rendered.Bytes(), source
}

func cloneNextValues(values map[string]interface{}) map[string]interface{} {
	return map[string]interface{}(value.Values(values).DeepCopy())
}

// reconcileNextValueSources inserts unresolved declared bundle inputs back into
// the ordered evidence. Files and FileSources stay aligned, including skipped
// inputs, and rendered temporary filenames never replace declared identities.
func reconcileNextValueSources(item *PackageValues, bundle []ValueSource, parsedInputs, operators int) {
	defaults := len(item.Sources) - parsedInputs
	if defaults < 0 {
		defaults = 0
	}
	sources := append([]ValueSource(nil), item.Sources[:defaults]...)
	parsed := defaults
	for _, source := range bundle {
		if source.Status == ValueResolved && parsed < len(item.Sources)-operators {
			source.Values = item.Sources[parsed].Values
			parsed++
		}
		sources = append(sources, source)
		if source.Status != ValueResolved {
			diagnostic := "Bundle values input remains unresolved."
			if len(source.Diagnostics) > 0 {
				diagnostic = source.Diagnostics[0].Message
			}
			item.Usages = append(item.Usages, PackageValueUsage{Path: source.Name, Runtime: true,
				Status: source.Status, Diagnostic: diagnostic,
				Locations: []ValueUsageLocation{{File: source.Name, Kind: "bundleValuesTemplate"}}})
		}
	}
	if operators > 0 {
		sources = append(sources, item.Sources[len(item.Sources)-operators:]...)
	}
	item.Files, item.FileSources = nil, nil
	for i := range sources {
		sources[i].Order = i + 1
		sources[i].ID = fmt.Sprintf("%s:%d", sources[i].Kind, i+1)
		item.Files = append(item.Files, sources[i].Name)
		filename := sources[i].MaterializedFile
		if filename == "" {
			filename = sources[i].Name
		}
		item.FileSources = append(item.FileSources, filename)
	}
	item.Sources = sources
	item.Scope = "Ordered resolved inspected files only; unresolved bundle inputs, --set-values, runtime setValues, and chart application are not evaluated."
	for i := range item.Usages {
		if !item.Usages[i].Runtime {
			item.Usages[i].Status, item.Usages[i].Diagnostic = "", ""
		}
	}
	finalizePackageValues(item)
}

func nextLiteralVariable(pipe *parse.PipeNode) (string, bool) {
	if pipe == nil || len(pipe.Decl) != 0 || len(pipe.Cmds) != 1 {
		return "", false
	}
	args := pipe.Cmds[0].Args
	if len(args) == 1 {
		field, ok := args[0].(*parse.FieldNode)
		if ok && len(field.Ident) > 1 && field.Ident[0] == "vars" {
			return "." + strings.Join(field.Ident[1:], "."), true
		}
	}
	if len(args) < 3 {
		return "", false
	}
	fn, ok := args[0].(*parse.IdentifierNode)
	if !ok || fn.Ident != "index" {
		return "", false
	}
	field, ok := args[1].(*parse.FieldNode)
	if !ok || len(field.Ident) == 0 || field.Ident[0] != "vars" {
		return "", false
	}
	parts := append([]string(nil), field.Ident[1:]...)
	for _, arg := range args[2:] {
		key, ok := arg.(*parse.StringNode)
		if !ok || key.Text == "" || strings.Contains(key.Text, ".") {
			return "", false
		}
		parts = append(parts, key.Text)
	}
	return "." + strings.Join(parts, "."), true
}
