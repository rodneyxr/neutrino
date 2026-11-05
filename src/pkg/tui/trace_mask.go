package tui

import (
	"regexp"
	"strings"
	"unicode"
)

const redactedValue = "[redacted]"

// Redaction is a display boundary: the borrowed inspection report and source
// snapshots remain untouched. Explicit variable/schema metadata wins; familiar
// credential paths are also masked when the source has no sensitivity metadata.
func (m Model) sensitiveValue(value DisplayItem) bool {
	if value.IsPkgVar || value.IsZarfVar {
		for _, variable := range m.pkgVariables {
			if variable.Name == value.Path && variable.Sensitive {
				return true
			}
		}
	}
	return sensitivePath(value.Path)
}

func (m Model) displayValue(value DisplayItem, raw interface{}) interface{} {
	if m.sensitiveValue(value) {
		return redactedValue
	}
	var schema map[string]interface{}
	path := value.Path
	if value.IsPackageValue || value.IsPackageMapping {
		if pkg := m.tracePackage(value); pkg != nil {
			schema = pkg.Schema
			if value.Component == "UDS Next bundle" {
				path = "."
			}
		}
	} else if !value.IsBundleVariable && !value.IsPkgVar && !value.IsZarfVar {
		// Keep the chart root available while resolving local references. A
		// detached property's $ref may name a definition outside that property.
		if result := m.components[value.Component]; result != nil {
			for _, declaration := range result.ValuesSchemas {
				chartSchema, _ := declaration.Schema.(map[string]interface{})
				prefix := packagePath(declaration.ValuePrefix)
				localPath := packagePath(path)
				if prefix != "" {
					if localPath == prefix {
						localPath = "."
					} else if strings.HasPrefix(localPath, prefix+".") {
						localPath = strings.TrimPrefix(localPath, prefix+".")
					} else {
						continue
					}
				}
				raw = traceSafeValueWithSchema(path, raw, schemaForPath(chartSchema, localPath))
				// Some charts expose wrapper defaults through public template
				// paths. The display projection must retain wrapper sensitivity
				// even though it does not copy private schema declarations.
				projection := normalizeMaskSchema(chartSchema)
				properties, _ := projection["properties"].(map[string]interface{})
				if _, declared := properties["_internal_defaults_do_not_set"]; declared {
					wrappedPath := "_internal_defaults_do_not_set"
					if packagePath(localPath) != "" {
						wrappedPath += "." + packagePath(localPath)
					}
					raw = traceSafeValueWithSchema(path, raw, schemaForPath(projection, wrappedPath))
				}
			}
		}
		if property, ok := value.Schema.(map[string]interface{}); ok {
			// Explicit annotations remain useful for older/incomplete reports.
			// Resolve detached references against the correctly prefixed chart.
			root, _ := m.selectedValueSchema(value).(map[string]interface{})
			raw = traceSafeValueWithSchema(path, raw, resolveMaskSchema(property, root, make(map[string]bool), 0))
		}
		for _, detail := range m.valuePolicy.details(value.Component, value.ValueUsage) {
			if detail.row.packagePath != "" && detail.pkg != nil {
				raw = traceSafeValueWithSchema(detail.row.packagePath, raw, schemaForPath(detail.pkg.Schema, detail.row.packagePath))
			}
		}
	}
	return traceSafeValueWithSchema(path, raw, schemaForPath(schema, path))
}

func traceSafeValue(path string, value interface{}) interface{} {
	return traceSafeValueWithSchema(path, value, nil)
}

func traceSafeValueWithSchema(path string, value interface{}, schema map[string]interface{}) interface{} {
	if sensitivePath(path) || schemaSensitive(schema) {
		return redactedValue
	}
	switch current := value.(type) {
	case map[string]interface{}:
		copy := make(map[string]interface{}, len(current))
		for key, child := range current {
			copy[key] = traceSafeValueWithSchema(strings.Trim(path+"."+key, "."), child, schemaForPath(schema, key))
		}
		return copy
	case []interface{}:
		copy := make([]interface{}, len(current))
		itemSchema, _ := schema["items"].(map[string]interface{})
		for i, child := range current {
			copy[i] = traceSafeValueWithSchema(path, child, itemSchema)
		}
		return copy
	default:
		return value
	}
}

func sensitivePath(path string) bool {
	for _, segment := range strings.FieldsFunc(path, func(r rune) bool { return r == '.' || r == '[' || r == ']' || r == '/' }) {
		key := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, segment)
		for _, suffix := range []string{"password", "passwd", "secret", "token", "privatekey", "apikey", "credential", "credentials"} {
			if strings.HasSuffix(key, suffix) {
				return true
			}
		}
	}
	return false
}

func schemaSensitive(schema map[string]interface{}) bool {
	writeOnly, _ := schema["writeOnly"].(bool)
	format, _ := schema["format"].(string)
	if writeOnly || format == "password" {
		return true
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		parts, _ := schema[keyword].([]interface{})
		for _, part := range parts {
			if object, ok := part.(map[string]interface{}); ok && schemaSensitive(object) {
				return true
			}
		}
	}
	return false
}

func schemaForPath(schema map[string]interface{}, path string) map[string]interface{} {
	schema = normalizeMaskSchema(schema)
	for _, segment := range strings.Split(packagePath(path), ".") {
		if segment == "" {
			continue
		}
		// Parent sensitivity covers every descendant.
		if schemaSensitive(schema) {
			return schema
		}
		properties, _ := schema["properties"].(map[string]interface{})
		child, _ := properties[segment].(map[string]interface{})
		patterns, _ := schema["patternProperties"].(map[string]interface{})
		for pattern, candidate := range patterns {
			if matched, err := regexp.MatchString(pattern, segment); err == nil && matched {
				constraint, _ := candidate.(map[string]interface{})
				child = mergeMaskSchemas(child, constraint)
			}
		}
		if child == nil {
			child, _ = schema["additionalProperties"].(map[string]interface{})
		}
		schema = child
	}
	return schema
}

// Schema examples/defaults may themselves contain credentials. Keep the schema
// structure useful without turning the schema disclosure toggle into a bypass.
func traceSafeSchema(schema interface{}) interface{} {
	root, _ := schema.(map[string]interface{})
	return maskSchemaDocument(schema, root, false, nil, 0)
}
