package introspect

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaInventoryKeepsAnnotationsWithoutApplyingThem(t *testing.T) {
	var schema interface{}
	require.NoError(t, json.Unmarshal([]byte(`{
  "required":["object"],
  "properties":{
    "object":{"required":["enabled"],"properties":{"enabled":{"default":false},"nullable":{"default":null}}},
    "annotation":{"default":"schema-only"},
    "dotted.key":{"default":"not a dot path"},
    "forbidden":false
  },
  "allOf":[{"properties":{"conditional":{"type":"string"}}}]
}`), &schema))
	before, err := json.Marshal(schema)
	require.NoError(t, err)
	properties := SchemaInventory(schema)
	var paths []string
	byPath := make(map[string]SchemaProperty)
	for _, property := range properties {
		paths = append(paths, property.Path)
		byPath[property.Path] = property
	}
	require.Equal(t, []string{"annotation", "forbidden", "object", "object.enabled", "object.nullable"}, paths)
	require.True(t, byPath["object"].Required)
	require.True(t, byPath["object.enabled"].Required)
	require.True(t, byPath["object.enabled"].DefaultPresent)
	require.Equal(t, false, byPath["object.enabled"].Default)
	require.True(t, byPath["object.nullable"].DefaultPresent)
	require.Nil(t, byPath["object.nullable"].Default)
	require.Equal(t, false, byPath["forbidden"].Schema)
	after, err := json.Marshal(schema)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestSourcePositionOmitsUnknownCoordinates(t *testing.T) {
	for _, test := range []struct {
		line, column int
		want         string
	}{
		{0, 0, "packaged-values"},
		{0, 5, "packaged-values"},
		{-1, -1, "packaged-values"},
		{12, 0, "packaged-values:12"},
		{12, 4, "packaged-values:12:4"},
	} {
		location := ValueUsageLocation{File: "packaged-values", Line: test.line, Column: test.column, Kind: "value"}
		require.Equal(t, test.want, location.Position())
		encoded, err := json.Marshal(location)
		require.NoError(t, err)
		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, test.want, decoded["location"])
		yamlValue, err := location.MarshalYAML()
		require.NoError(t, err)
		// Both encoders share Position; JSON can inspect the anonymous YAML
		// result without depending on a YAML library's quoting conventions.
		encoded, err = json.Marshal(yamlValue)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, test.want, decoded["Location"])
	}
}
