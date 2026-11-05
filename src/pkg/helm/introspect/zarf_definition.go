package introspect

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/zarf-dev/zarf/src/api"
	"github.com/zarf-dev/zarf/src/api/convert"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/api/v1beta1"
)

// zarfDefinition retains the authored optionality needed by the Package Reader.
// An omitted alpha required field does not permit a missing component payload,
// even though the normalized deployment model treats that component as optional.
type zarfDefinition struct {
	api.Package
	optionalComponents map[string]bool
}

// parseZarfDefinition follows Zarf's supported-document selection rules without
// loading resources, validating deployment requirements, or executing actions.
func parseZarfDefinition(contents []byte) (zarfDefinition, error) {
	file, err := parser.ParseBytes(contents, 0)
	if err != nil {
		return zarfDefinition{}, err
	}

	var selected ast.Node
	var selectedVersion string
	seenVersions := map[string]bool{}
	hasDocument := false
	for i, doc := range file.Docs {
		if doc == nil || doc.Body == nil {
			continue
		}
		hasDocument = true
		var probe struct {
			APIVersion string `yaml:"apiVersion"`
		}
		if err := yaml.NodeToValue(doc.Body, &probe); err != nil {
			return zarfDefinition{}, fmt.Errorf("document %d: reading apiVersion: %w", i+1, err)
		}
		version := probe.APIVersion
		if version == "" {
			version = v1alpha1.APIVersion
		}
		if version != v1alpha1.APIVersion && version != v1beta1.APIVersion {
			continue
		}
		if seenVersions[version] {
			return zarfDefinition{}, fmt.Errorf("duplicate apiVersion %q in package definition", version)
		}
		seenVersions[version] = true
		if selected == nil || version == v1beta1.APIVersion {
			selected = doc.Body
			selectedVersion = version
		}
	}
	if !hasDocument {
		return zarfDefinition{}, fmt.Errorf("no package definition found")
	}
	if selected == nil {
		return zarfDefinition{}, fmt.Errorf("no supported apiVersion found in package definition")
	}

	definition := zarfDefinition{optionalComponents: map[string]bool{}}
	switch selectedVersion {
	case v1alpha1.APIVersion:
		var native v1alpha1.ZarfPackage
		if err := yaml.NodeToValue(selected, &native); err != nil {
			return zarfDefinition{}, fmt.Errorf("decoding %s package definition: %w", selectedVersion, err)
		}
		definition.Package = convert.PackageFromV1alpha1(native)
		for _, component := range native.Components {
			definition.optionalComponents[component.Name] = component.Required != nil && !*component.Required
		}
	case v1beta1.APIVersion:
		var native v1beta1.Package
		if err := yaml.NodeToValue(selected, &native); err != nil {
			return zarfDefinition{}, fmt.Errorf("decoding %s package definition: %w", selectedVersion, err)
		}
		definition.Package = convert.PackageFromV1beta1(native)
		for _, component := range native.Components {
			definition.optionalComponents[component.Name] = component.Optional
		}
	}
	return definition, nil
}
