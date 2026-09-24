package introspect

import (
	"strings"
	"testing"
)

func TestNextDefinitionLocalsAndDependencyOrder(t *testing.T) {
	input := []byte(`uds { bundle_api_version = "uds.dev/v1alpha1" }
locals { source = "../package.tar.zst" }
metadata {
 name = "example"
 version = "1.0.0"
}
package "second" {
 source = local.source
 depends_on = [package.first]
 values_files = ["one.yaml", "two.yaml"]
}
package "first" { source = local.source }
`)
	def, err := parseNextDefinition(input, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := nextPackageOrder(def)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 2 || ordered[0].ID != "first" || ordered[1].ID != "second" {
		t.Fatalf("incorrect dependency order: %+v", ordered)
	}
	if ordered[1].Source != "../package.tar.zst" || strings.Join(ordered[1].ValuesFiles, ",") != "one.yaml,two.yaml" {
		t.Fatalf("lost source or values order: %+v", ordered[1])
	}
}
func TestNextDefinitionRejectsBrokenGraph(t *testing.T) {
	for _, tt := range []struct{ name, packages, want string }{
		{"missing", "package \"one\" {\n source = \"x\"\n depends_on = [package.absent]\n}", "unknown bundle package dependency"},
		{"cycle", "package \"one\" {\n source = \"x\"\n depends_on = [package.two]\n}\npackage \"two\" {\n source = \"x\"\n depends_on = [package.one]\n}", "dependency cycle"},
		{"duplicate", "package \"one\" { source = \"x\" }\npackage \"one\" { source = \"x\" }", "duplicate bundle package"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			def, err := parseNextDefinition([]byte("uds { bundle_api_version = \"uds.dev/v1alpha1\" }\nmetadata { name = \"example\" }\n"+tt.packages), "amd64")
			if err != nil {
				t.Fatal(err)
			}
			_, err = nextPackageOrder(def)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q; got %v", tt.want, err)
			}
		})
	}
}
func TestNextVariablesNestedConfigMerge(t *testing.T) {
	defaults, err := nextVariables([]byte(`variables = { site = { host = "initial", port = 80 }, ports = [1,2] }`), nextDefaultsFile)
	if err != nil {
		t.Fatal(err)
	}
	config, err := nextVariables([]byte(`options { architecture = "amd64" }
variables = { site = { host = "configured" }, ports = [3] }`), "config.uds.hcl")
	if err != nil {
		t.Fatal(err)
	}
	merged := nextMerge(defaults, config)
	site := merged["site"].(map[string]interface{})
	if site["host"] != "configured" || site["port"] != float64(80) {
		t.Fatalf("wrong nested merge: %+v", site)
	}
	if ports := merged["ports"].([]interface{}); len(ports) != 1 || ports[0] != float64(3) {
		t.Fatalf("list not replaced: %+v", ports)
	}
}

func TestNextSignatureVerificationMetadataDoesNotExecute(t *testing.T) {
	for _, signature := range []string{
		"signature_verification { public_key = \"unread-key.pem\" }",
		"signature_verification {\n keyless { certificate_identity = \"example\" }\n}",
	} {
		source := "uds { bundle_api_version = \"uds.dev/v1alpha1\" }\nmetadata { name = \"example\" }\npackage \"app\" {\n source = \"unused\"\n" + signature + "\n}\n"
		def, err := parseNextDefinition([]byte(source), "amd64")
		if err != nil {
			t.Fatalf("signature block %q: %v", signature, err)
		}
		if len(def.Packages) != 1 || def.Packages[0].ID != "app" {
			t.Fatalf("package metadata lost: %+v", def.Packages)
		}
	}
}
