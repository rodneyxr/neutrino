package introspect

import (
	"strings"

	"github.com/zarf-dev/zarf/src/pkg/value"
)

// finalizePackageValues annotates evidence without applying mappings to chart defaults.
func finalizePackageValues(values *PackageValues) {
	for i := range values.Usages {
		usage := &values.Usages[i]
		if usage.Status != "" {
			continue
		}
		if usage.Runtime {
			usage.Status = ValueDynamic
			usage.Diagnostic = "Requires deployment-time template or action evaluation."
			for _, location := range usage.Locations {
				switch location.Kind {
				case "unavailableTemplate":
					usage.Status = ValueUnavailable
					usage.Diagnostic = "Template content is outside the inspected Package View."
				case "unparsedTemplate":
					usage.Status = ValueParseError
					usage.Diagnostic = "Template could not be parsed statically."
				case "setValues":
					usage.Diagnostic = "Written by an action at deployment time; action was not executed."
				}
			}
			continue
		}
		usage.Status = ValueResolved
		path := "." + strings.TrimPrefix(usage.Path, ".")
		if _, err := value.Values(values.Values).Extract(value.Path(path)); err != nil {
			usage.Status = ValueMissing
			usage.Diagnostic = "Mapping source is absent from the inspected Package Values."
		}
		for _, source := range values.Sources {
			if source.Status != "" && source.Status != ValueResolved {
				usage.Status = source.Status
				usage.Diagnostic = "An unresolved values input may affect this mapping source."
			}
		}
	}
}

func syncPackageValueMappings(report *ZarfPackageIntrospection) {
	for _, chart := range report.ChartResults {
		for i := range chart.PackageValueMappings {
			mapping := &chart.PackageValueMappings[i]
			for _, values := range report.PackageValues {
				for _, usage := range values.Usages {
					if !usage.Runtime && usage.ComponentName == mapping.ComponentName && usage.ChartName == mapping.ChartName &&
						usage.Path == mapping.Path && usage.TargetPath == mapping.TargetPath {
						mapping.Status, mapping.Diagnostic = usage.Status, usage.Diagnostic
					}
				}
			}
		}
	}
}
