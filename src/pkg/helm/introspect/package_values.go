package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/zarf-dev/zarf/src/api"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
	"github.com/zarf-dev/zarf/src/pkg/value"
)

// inspectPackageValues distinguishes source declarations from assembled package
// layouts: Zarf writes merged values.yaml and values.schema.json at root.
func inspectPackageValues(ctx context.Context, root, source string, sourceDirectory bool, pkg api.Package, overrides []string) (PackageValues, error) {
	result := PackageValues{PackageName: pkg.Metadata.Name, APIVersion: pkg.GetAPIVersion(), Values: map[string]interface{}{},
		Scope: "Ordered inspected files only; --set-values, runtime setValues, and chart application are not evaluated."}
	var defaults []string
	assembled := !sourceDirectory
	if assembled {
		if len(pkg.Values.Files) > 0 {
			defaults = []string{filepath.Join(root, layout.ValuesYAML)}
		}
	} else {
		for _, filename := range pkg.Values.Files {
			defaults = append(defaults, filepath.Join(source, filename))
		}
	}
	paths := append(append([]string(nil), defaults...), overrides...)
	merged := value.Values{}
	for i, filename := range paths {
		kind, name := "operator-file", filename
		if i < len(defaults) {
			kind = "packaged-default"
			if assembled {
				name = layout.ValuesYAML
			} else {
				name = pkg.Values.Files[i]
			}
		}
		layer := ValueSource{ID: fmt.Sprintf("%s:%d", kind, i+1), Kind: kind, Name: name,
			Order: i + 1, Status: ValueResolved, MaterializedFile: filename}
		if _, err := os.Stat(filename); err != nil {
			layer.Status = ValueUnavailable
			if os.IsNotExist(err) {
				layer.Status = ValueMissing
			}
			layer.Diagnostics = []ValueDiagnostic{{Status: layer.Status, Message: "Required values input could not be read."}}
			result.Sources = append(result.Sources, layer)
			return result, fmt.Errorf("required package values file %q: %w", filename, err)
		}
		vals, err := value.ParseFiles(ctx, []string{filename}, value.ParseFilesOptions{})
		if err != nil {
			layer.Status = ValueParseError
			layer.Diagnostics = []ValueDiagnostic{{Status: layer.Status, Message: "Values input could not be parsed as YAML."}}
			result.Sources = append(result.Sources, layer)
			return result, fmt.Errorf("failed to parse package values: %w", err)
		}
		layer.Values = map[string]interface{}(vals.DeepCopy())
		result.Sources = append(result.Sources, layer)
		merged.DeepMerge(vals)
	}
	result.Values = map[string]interface{}(merged)
	if assembled {
		if len(defaults) > 0 {
			result.Files = append(result.Files, layout.ValuesYAML)
		}
	} else {
		result.Files = append(result.Files, pkg.Values.Files...)
	}
	result.Files = append(result.Files, overrides...)
	result.FileSources = append(result.FileSources, defaults...)
	result.FileSources = append(result.FileSources, overrides...)
	schemaPath := filepath.Join(source, pkg.Values.Schema)
	if assembled {
		schemaPath = filepath.Join(root, layout.ValuesSchema)
	}
	if pkg.Values.Schema != "" || assembled {
		contents, err := os.ReadFile(schemaPath)
		if err != nil {
			if pkg.Values.Schema != "" || !os.IsNotExist(err) {
				return result, fmt.Errorf("required package values schema %q: %w", schemaPath, err)
			}
		} else if err := json.Unmarshal(contents, &result.Schema); err != nil {
			return result, fmt.Errorf("invalid package values schema %q: %w", schemaPath, err)
		}
	}
	return result, nil
}

func inspectTemplateText(text, filename, component, chart string) []PackageValueUsage {
	parsed, err := template.New(filename).Funcs(helmFuncMap()).Parse(text)
	if err != nil {
		return []PackageValueUsage{{Path: ".", ComponentName: component, ChartName: chart, Runtime: true, Status: ValueParseError,
			Diagnostic: "Template could not be parsed statically.",
			Locations:  []ValueUsageLocation{{File: filename, Kind: "unparsedTemplate"}}}}
	}
	walker := newWalker(chart, filename, text)
	for _, assoc := range parsed.Templates() {
		if assoc.Tree != nil && assoc.Tree.Root != nil {
			walker.walk(assoc.Tree.Root)
		}
	}
	usages := make([]PackageValueUsage, 0, len(walker.Results))
	for _, found := range walker.Results {
		usages = append(usages, PackageValueUsage{
			Path: "." + strings.TrimPrefix(found.Path, "."), ComponentName: component,
			ChartName: chart, Locations: found.Locations, Runtime: true, Status: ValueDynamic,
			Diagnostic: "Package template reference; deployment-time evaluation is not performed.",
		})
	}
	// The Helm walker intentionally follows .Values. Retain other Zarf template
	// contexts too, so a .State-only action does not disappear from inspection.
	seen := map[string]bool{}
	addContext := func(parts []string) {
		if len(parts) == 0 {
			return
		}
		switch parts[0] {
		case "State", "Variables", "Constants", "Pkg":
		default:
			return
		}
		path := "." + strings.Join(parts, ".")
		if seen[path] {
			return
		}
		seen[path] = true
		usages = append(usages, PackageValueUsage{Path: path, ComponentName: component, ChartName: chart,
			Runtime: true, Status: ValueDynamic, Diagnostic: "Zarf template context requires deployment-time evaluation.",
			Locations: []ValueUsageLocation{{File: filename, Kind: "runtimeContext"}}})
	}
	var walk func(parse.Node)
	walk = func(node parse.Node) {
		switch n := node.(type) {
		case *parse.ListNode:
			if n != nil {
				for _, child := range n.Nodes {
					walk(child)
				}
			}
		case *parse.ActionNode:
			walk(n.Pipe)
		case *parse.PipeNode:
			if n != nil {
				for _, command := range n.Cmds {
					walk(command)
				}
			}
		case *parse.CommandNode:
			if len(n.Args) >= 3 {
				identifier, isIdentifier := n.Args[0].(*parse.IdentifierNode)
				field, isField := n.Args[1].(*parse.FieldNode)
				if isIdentifier && identifier.Ident == "index" && isField && len(field.Ident) > 0 && field.Ident[0] != "Values" {
					parts := append([]string(nil), field.Ident...)
					literal := true
					for _, argument := range n.Args[2:] {
						key, ok := argument.(*parse.StringNode)
						if !ok || strings.Contains(key.Text, ".") {
							literal = false
							break
						}
						parts = append(parts, key.Text)
					}
					if literal {
						addContext(parts)
						return
					}
				}
			}
			for _, arg := range n.Args {
				walk(arg)
			}
		case *parse.FieldNode:
			addContext(n.Ident)
		case *parse.ChainNode:
			walk(n.Node)
		case *parse.IfNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.TemplateNode:
			walk(n.Pipe)
		}
	}
	for _, associated := range parsed.Templates() {
		if associated.Tree != nil {
			walk(associated.Tree.Root)
		}
	}
	if len(usages) == 0 {
		usages = append(usages, PackageValueUsage{Path: ".", ComponentName: component, ChartName: chart,
			Runtime: true, Status: ValueDynamic, Diagnostic: "Template is enabled for runtime evaluation; no statically named configuration reference was found.",
			Locations: []ValueUsageLocation{{File: filename, Kind: "runtimeTemplate"}}})
	}
	return usages
}

func inspectComponentRuntime(component api.Component, root, source string, sourceDirectory bool) []PackageValueUsage {
	var uses []PackageValueUsage
	for _, actionSet := range []api.ActionSet{component.Actions.OnDeploy, component.Actions.OnRemove} {
		for _, actions := range [][]api.Action{actionSet.Before, actionSet.After, actionSet.OnSuccess, actionSet.OnFailure} {
			for _, action := range actions {
				if action.EnableTemplating {
					uses = append(uses, inspectTemplateText(action.Cmd, "actions", component.Name, "")...)
				}
				for _, set := range action.SetValues {
					uses = append(uses, PackageValueUsage{Path: set.Key, ComponentName: component.Name, Runtime: true,
						Locations: []ValueUsageLocation{{File: "actions", Kind: "setValues"}}})
				}
			}
		}
	}
	for _, manifest := range component.Manifests {
		if !manifest.EnableTemplating {
			continue
		}
		for _, file := range manifest.Files {
			filename := filepath.Join(source, file)
			if !sourceDirectory {
				filename = filepath.Join(root, file)
			}
			contents, err := os.ReadFile(filename)
			if err == nil {
				uses = append(uses, inspectTemplateText(string(contents), file, component.Name, manifest.Name)...)
			} else {
				// Assembled component payloads may not be in the chart-only Package View.
				// Record the unresolved template instead of pretending it has no usages.
				uses = append(uses, PackageValueUsage{Path: ".", ComponentName: component.Name, ChartName: manifest.Name, Runtime: true,
					Locations: []ValueUsageLocation{{File: file, Kind: "unavailableTemplate"}}})
			}
		}
	}
	for _, file := range component.Files {
		if !file.EnableTemplating {
			continue
		}
		filename := filepath.Join(source, file.Source)
		if !sourceDirectory {
			filename = filepath.Join(root, file.Source)
		}
		contents, err := os.ReadFile(filename)
		if err == nil {
			uses = append(uses, inspectTemplateText(string(contents), file.Source, component.Name, "")...)
		} else {
			uses = append(uses, PackageValueUsage{Path: ".", ComponentName: component.Name, Runtime: true,
				Locations: []ValueUsageLocation{{File: file.Source, Kind: "unavailableTemplate"}}})
		}
	}
	return uses
}
