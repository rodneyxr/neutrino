package tui

import "strings"

const maskSchemaResolved = "\x00neutrino-mask-resolved"

// This is a conservative projection of sensitivity annotations, not a JSON
// Schema validator. Any alternative may mark an input private. Unknown/cyclic
// references mask their affected subtree instead of risking disclosure.
func normalizeMaskSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil || schema[maskSchemaResolved] == true {
		return schema
	}
	return resolveMaskSchema(schema, schema, make(map[string]bool), 0)
}

func resolveMaskSchema(node, root map[string]interface{}, refs map[string]bool, depth int) map[string]interface{} {
	if node == nil {
		return nil
	}
	if depth > 32 {
		return map[string]interface{}{maskSchemaResolved: true, "writeOnly": true}
	}
	result := map[string]interface{}{maskSchemaResolved: true}
	if schemaSensitive(node) {
		result["writeOnly"] = true
	}
	if reference, _ := node["$ref"].(string); reference != "" {
		target := root
		valid := strings.HasPrefix(reference, "#/")
		if reference == "#" {
			valid = true
		} else if valid {
			var current interface{} = root
			for _, segment := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
				segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
				object, _ := current.(map[string]interface{})
				current = object[segment]
			}
			target, valid = current.(map[string]interface{})
		}
		if !valid || refs[reference] {
			result["writeOnly"] = true
		} else {
			refs[reference] = true
			result = mergeMaskSchemas(result, resolveMaskSchema(target, root, refs, depth+1))
			delete(refs, reference)
		}
	}
	for _, keyword := range []string{"properties", "patternProperties"} {
		properties, _ := node[keyword].(map[string]interface{})
		if len(properties) == 0 {
			continue
		}
		children := make(map[string]interface{}, len(properties))
		for name, child := range properties {
			definition, _ := child.(map[string]interface{})
			children[name] = resolveMaskSchema(definition, root, refs, depth+1)
		}
		result = mergeMaskSchemas(result, map[string]interface{}{keyword: children})
	}
	for _, keyword := range []string{"items", "additionalProperties", "then", "else"} {
		child, _ := node[keyword].(map[string]interface{})
		if child == nil {
			continue
		}
		resolved := resolveMaskSchema(child, root, refs, depth+1)
		if keyword == "then" || keyword == "else" {
			result = mergeMaskSchemas(result, resolved)
		} else {
			result = mergeMaskSchemas(result, map[string]interface{}{keyword: resolved})
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		parts, _ := node[keyword].([]interface{})
		for _, part := range parts {
			object, _ := part.(map[string]interface{})
			result = mergeMaskSchemas(result, resolveMaskSchema(object, root, refs, depth+1))
		}
	}
	return result
}

func mergeMaskSchemas(left, right map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(left)+len(right)+1)
	result[maskSchemaResolved] = true
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		if existing, ok := result[key].(map[string]interface{}); ok {
			if incoming, ok := value.(map[string]interface{}); ok {
				result[key] = mergeMaskSchemas(existing, incoming)
				continue
			}
		}
		if key == "writeOnly" && result[key] == true {
			continue
		}
		result[key] = value
	}
	return result
}

func maskSchemaDocument(raw interface{}, root map[string]interface{}, inherited bool, projection map[string]interface{}, depth int) interface{} {
	if depth > 64 {
		return redactedValue
	}
	if list, ok := raw.([]interface{}); ok {
		result := make([]interface{}, len(list))
		for i, child := range list {
			result[i] = maskSchemaDocument(child, root, inherited, projection, depth+1)
		}
		return result
	}
	node, ok := raw.(map[string]interface{})
	if !ok {
		return raw
	}
	projection = mergeMaskSchemas(projection, resolveMaskSchema(node, root, make(map[string]bool), 0))
	hidden := inherited || schemaSensitive(projection)
	result := make(map[string]interface{}, len(node))
	for key, value := range node {
		switch key {
		case "default", "example", "examples", "const", "enum":
			if hidden {
				result[key] = redactedValue
			} else if key == "examples" || key == "enum" {
				if values, ok := value.([]interface{}); ok {
					masked := make([]interface{}, len(values))
					for i, item := range values {
						masked[i] = traceSafeValueWithSchema("", item, projection)
					}
					result[key] = masked
				} else {
					result[key] = traceSafeValueWithSchema("", value, projection)
				}
			} else {
				result[key] = traceSafeValueWithSchema("", value, projection)
			}
		case "properties":
			properties, _ := value.(map[string]interface{})
			children := make(map[string]interface{}, len(properties))
			for name, child := range properties {
				children[name] = maskSchemaDocument(child, root, hidden || sensitivePath(name), schemaForPath(projection, name), depth+1)
			}
			result[key] = children
		default:
			result[key] = maskSchemaDocument(value, root, hidden, nil, depth+1)
		}
	}
	return result
}
