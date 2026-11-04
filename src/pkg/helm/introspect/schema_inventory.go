package introspect

import (
	"sort"
	"strings"
)

// SchemaProperty describes an explicitly named property. Default is an
// annotation only: schema inventory never applies defaults or validates values.
type SchemaProperty struct {
	Path           string
	Schema         interface{}
	Required       bool
	Default        interface{}
	DefaultPresent bool
}

// SchemaInventory returns explicit properties in stable path order, including
// properties without configured values. It follows nested properties only;
// references, composition, conditions, patterns and array item schemas remain
// available in the original schema, without inventing concrete property paths.
// Dotted keys are omitted under the existing dot-notation path convention.
func SchemaInventory(schema interface{}) []SchemaProperty {
	var properties []SchemaProperty
	var walk func(interface{}, string)
	walk = func(raw interface{}, prefix string) {
		node, ok := raw.(map[string]interface{})
		if !ok {
			return
		}
		children, ok := node["properties"].(map[string]interface{})
		if !ok {
			return
		}
		required := make(map[string]bool)
		switch names := node["required"].(type) {
		case []interface{}:
			for _, name := range names {
				if name, ok := name.(string); ok {
					required[name] = true
				}
			}
		case []string:
			for _, name := range names {
				required[name] = true
			}
		}
		keys := make([]string, 0, len(children))
		for name := range children {
			if name != "" && !strings.Contains(name, ".") {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		for _, name := range keys {
			property := SchemaProperty{
				Path:     joinValuePath(prefix, name),
				Schema:   children[name],
				Required: required[name],
			}
			if child, ok := children[name].(map[string]interface{}); ok {
				property.Default, property.DefaultPresent = child["default"]
			}
			properties = append(properties, property)
			walk(children[name], property.Path)
		}
	}
	walk(schema, "")
	return properties
}
