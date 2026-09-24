package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
	"github.com/zarf-dev/zarf/src/pkg/value"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// inspectPackageValues distinguishes source declarations from assembled package
// layouts: Zarf v0.82.0 writes merged values.yaml and values.schema.json at root.
func inspectPackageValues(ctx context.Context, root, source string, sourceDirectory bool, pkg v1alpha1.ZarfPackage, overrides []string) (PackageValues, error) {
	result := PackageValues{PackageName: pkg.Metadata.Name, Values: map[string]interface{}{}}
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
	for _, filename := range paths {
		if _, err := os.Stat(filename); err != nil {
			return result, fmt.Errorf("required package values file %q: %w", filename, err)
		}
	}
	if len(paths) > 0 {
		vals, err := value.ParseFiles(ctx, paths, value.ParseFilesOptions{})
		if err != nil {
			return result, fmt.Errorf("failed to parse package values: %w", err)
		}
		result.Values = map[string]interface{}(vals)
	}
	result.Files = append(result.Files, pkg.Values.Files...)
	result.Files = append(result.Files, overrides...)
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
		return []PackageValueUsage{{Path: ".", ComponentName: component, ChartName: chart, Runtime: true,
			Locations: []ValueUsageLocation{{File: filename, Kind: "unparsedTemplate"}}}}
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
			ChartName: chart, Locations: found.Locations, Runtime: true,
		})
	}
	return usages
}


func inspectComponentRuntime(component v1alpha1.ZarfComponent, root, source string, sourceDirectory bool) []PackageValueUsage {
	var uses []PackageValueUsage
	for _, actionSet := range []v1alpha1.ZarfComponentActionSet{component.Actions.OnDeploy, component.Actions.OnRemove} {
		for _, actions := range [][]v1alpha1.ZarfComponentAction{actionSet.Before, actionSet.After, actionSet.OnSuccess, actionSet.OnFailure} {
			for _, action := range actions {
				if action.ShouldTemplate() {
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
		if !manifest.IsTemplate() {
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
		if !file.IsTemplate() {
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
