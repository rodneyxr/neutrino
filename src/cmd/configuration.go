package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/goccy/go-yaml"
	"github.com/zarf-dev/zarf/src/pkg/message"
)

func printPackageConfiguration(out io.Writer, report introspect.InspectionReport) error {
	if bundle := report.Bundle; bundle != nil {
		fmt.Fprintf(out, "\n=== UDS Bundle: %s (%s, %s) ===\n", bundle.Name, bundle.Version, bundle.Format)
		var rows [][]string
		for _, pkg := range bundle.Packages {
			rows = append(rows, []string{pkg.ID, pkg.Source, strings.Join(pkg.DependsOn, ", "), pkg.Namespace, strings.Join(pkg.ValuesFiles, ", ")})
		}
		message.TableWithWriter(out, []string{"Package", "Source", "Depends On", "Namespace", "Values Files"}, rows)
		if len(bundle.Variables) > 0 {
			fmt.Fprintln(out, "Bundle variables:")
			if err := printConfigurationMap(out, bundle.Variables); err != nil {
				return err
			}
		}
	}
	for _, pkg := range report.PackageValues {
		fmt.Fprintf(out, "\n=== Zarf Package Values: %s ===\n", pkg.PackageName)
		if len(pkg.Files) > 0 {
			fmt.Fprintf(out, "Files (in precedence order): %s\n", strings.Join(pkg.Files, ", "))
		}
		if err := printConfigurationMap(out, pkg.Values); err != nil {
			return err
		}
		if len(pkg.Schema) > 0 {
			fmt.Fprintln(out, "Package values schema:")
			if err := printConfigurationMap(out, pkg.Schema); err != nil {
				return err
			}
		}
		var rows [][]string
		for _, usage := range pkg.Usages {
			state := "static"
			if usage.Runtime {
				state = "runtime / unresolved"
			}
			var locations []string
			for _, loc := range usage.Locations {
				locations = append(locations, fmt.Sprintf("%s:%d:%d", loc.File, loc.Line, loc.Column))
			}
			rows = append(rows, []string{usage.Path, strings.Trim(usage.ComponentName+"/"+usage.ChartName, "/"), usage.TargetPath, strings.Join(usage.ExcludePaths, ", "), state, strings.Join(locations, ", ")})
		}
		if len(rows) > 0 {
			message.TableWithWriter(out, []string{"Package Path", "Component/Chart", "Helm Target", "Excluded Paths", "Resolution", "Location"}, rows)
		}
	}
	return nil
}

func printConfigurationMap(out io.Writer, values map[string]interface{}) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// Encode each subtree as YAML so structured values and false/zero/null remain distinct.
		data, err := yaml.Marshal(map[string]interface{}{key: values[key]})
		if err != nil {
			return err
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
	}
	return nil
}
