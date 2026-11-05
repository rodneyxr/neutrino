package introspect

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/zarf-dev/zarf/src/api"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
)

// Packaged charts never fall back to source paths or URLs. Selection and layout
// are shared by verified descriptor validation and materialized consumption.
func selectPackagedComponent(component api.Component, available, explicitlyOptional bool) (string, bool, error) {
	if len(component.Charts) == 0 {
		return "", false, nil
	}
	if component.Name == "" || strings.Contains(component.Name, "\\") || path.Base(component.Name) != component.Name || component.Name == "." || component.Name == ".." {
		return "", false, fmt.Errorf("unsupported package layout: invalid selected component name %q", component.Name)
	}
	title := path.Join("components", component.Name+".tar")
	if !available {
		if explicitlyOptional {
			return title, false, nil
		}
		return "", false, fmt.Errorf("incomplete package layout: missing selected descriptor %q for component %q", title, component.Name)
	}
	return title, true, nil
}

type packagedChart struct {
	ChartPath   string
	ValuesPaths []string
}

func readComponentEntries(title string, contents []byte) (map[string]byte, error) {
	entries := make(map[string]byte)
	reader := tar.NewReader(bytes.NewReader(contents))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("unsupported selected descriptor %q component archive: %w", title, err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		cleanName := path.Clean(name)
		if cleanName == "." {
			continue
		}
		if cleanName != name || path.IsAbs(name) || strings.Contains(name, "\\") || strings.HasPrefix(cleanName, "../") {
			return nil, fmt.Errorf("unsupported selected descriptor %q component archive entry %q", title, header.Name)
		}
		entries[cleanName] = header.Typeflag
	}
}

func packagedChartLayout(component api.Component, title string, entries map[string]byte) ([]packagedChart, error) {
	chartsBase, valuesBase := "charts", "values"
	if !hasArchivePrefix(entries, chartsBase+"/") {
		chartsBase = path.Join(component.Name, chartsBase)
		valuesBase = path.Join(component.Name, valuesBase)
	}
	charts := make([]packagedChart, 0, len(component.Charts))
	for _, chart := range component.Charts {
		selected := packagedChart{}
		for _, candidate := range []string{
			path.Join(chartsBase, layout.ChartArchiveName(chart.Name, chart.LegacyVersion)),
			path.Join(chartsBase, chart.Name+".tgz"),
			path.Join(chartsBase, chart.Name, "Chart.yaml"),
		} {
			if isRegularArchiveEntry(entries, candidate) {
				selected.ChartPath = candidate
				if path.Base(candidate) == "Chart.yaml" {
					selected.ChartPath = path.Dir(candidate)
				}
				break
			}
		}
		if selected.ChartPath == "" {
			return nil, fmt.Errorf("incomplete selected descriptor %q: declared chart %q was not found", title, chart.Name)
		}
		count := len(chart.ValuesFiles)
		selected.ValuesPaths = make([]string, 0, count)
		for i := range count {
			valuesPath := path.Join(valuesBase, layout.ChartValuesFileName(chart.Name, chart.LegacyVersion, i))
			if !isRegularArchiveEntry(entries, valuesPath) {
				return nil, fmt.Errorf("incomplete selected descriptor %q: managed values file %d for chart %q was not found", title, i, chart.Name)
			}
			selected.ValuesPaths = append(selected.ValuesPaths, valuesPath)
		}
		charts = append(charts, selected)
	}
	return charts, nil
}

func hasArchivePrefix(entries map[string]byte, prefix string) bool {
	for name := range entries {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isRegularArchiveEntry(entries map[string]byte, name string) bool {
	typeflag, exists := entries[name]
	return exists && (typeflag == tar.TypeReg || typeflag == tar.TypeRegA)
}
