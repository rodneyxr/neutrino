package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/goccy/go-yaml"
	"github.com/rodneyxr/neutrino/src/pkg/helm/introspect"
	"github.com/rodneyxr/neutrino/src/pkg/tui"
	"github.com/spf13/cobra"
	"github.com/zarf-dev/zarf/src/pkg/logger"
	"github.com/zarf-dev/zarf/src/pkg/message"
)

func init() {
	rootCmd.AddCommand(newInspectCmd(os.Stdout))
}

func newInspectCmd(out io.Writer) *cobra.Command {
	var (
		version          string
		outputFormat     string
		includeSubcharts bool
		sortMode         string
		architecture     string
		logLevel         string
		noCache          bool
		wildcards        bool
		absolute         bool
		noValues         bool
		interactive      bool
	)

	cmd := &cobra.Command{
		Use:   "inspect [PACKAGE_SOURCE]",
		Short: "Inspect a Zarf package or helm chart to find overridable values",
		Long:  `Analyzes Zarf package or helm chart templates to find which values from .Values are actually used.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Initialize logger
			lvl, err := logger.ParseLevel(logLevel)
			if err != nil {
				return err
			}
			logConfig := logger.ConfigDefault()
			logConfig.Level = lvl
			l, err := logger.New(logConfig)
			if err != nil {
				return err
			}
			ctx := logger.WithContext(cmd.Context(), l)

			source := args[0]
			opts := introspect.IntrospectOptions{
				IncludeSubcharts: includeSubcharts,
				NoCache:          noCache,
				NoValues:         noValues,
			}

			report := introspect.InspectionReport{
				Components: make(map[string]*introspect.IntrospectResult),
			}

			// Helper to filter and add result
			addResult := func(componentName string, res *introspect.IntrospectResult) {
				// Filter wildcards if not requested
				if !wildcards {
					var filteredValues []introspect.ValueUsage
					for _, v := range res.Values {
						if !v.Wildcard {
							filteredValues = append(filteredValues, v)
						}
					}
					res.Values = filteredValues
				}

				if sortMode == "path" {
					sort.Slice(res.Values, func(i, j int) bool {
						return res.Values[i].Path < res.Values[j].Path
					})
				}

				report.Components[componentName] = res
			}

			if introspect.IsRemote(source) {
				l.Info(fmt.Sprintf("Connecting to remote OCI registry %s...", source))
			}

			// Determine if it's a Zarf package or a UDS Bundle
			isZarf, err := introspect.IsZarfPackage(ctx, source, architecture)
			if err != nil {
				return fmt.Errorf("failed to determine source type: %w", err)
			}
			isUDS, err := introspect.IsUDSBundle(ctx, source, architecture)
			if err != nil {
				return fmt.Errorf("failed to determine source type: %w", err)
			}

			if isUDS {
				bundleInspection, err := introspect.InspectUDSBundle(ctx, source, version, architecture, opts)
				if err != nil {
					return err
				}

				report.Variables = bundleInspection.Variables
				report.ComponentOrder = bundleInspection.ComponentOrder

				keys := bundleInspection.ComponentOrder

				for _, key := range keys {
					addResult(key, bundleInspection.ChartResults[key])
				}
			} else if isZarf {
				pkgInspection, err := introspect.InspectZarfPackage(ctx, source, version, architecture, opts)
				if err != nil {
					return err
				}

				report.Variables = pkgInspection.Variables
				report.ComponentOrder = pkgInspection.ComponentOrder

				// Provide stable chronologic output ordering
				keys := pkgInspection.ComponentOrder

				for _, key := range keys {
					addResult(key, pkgInspection.ChartResults[key])
				}

				// We need to output variables too?
				// The previous code printed variables separately.
				// For JSON/YAML, we want them in the object.
				// For Table, we should specific format.

				// Let's handle output printing at the end.
				// But first let's finish gathering logic.

			} else {
				// Handle Helm chart
				src := introspect.ChartSource{
					Version: version,
				}

				if introspect.IsRemote(source) {
					// Handle OCI tags if present (e.g. oci://...:tag)
					if strings.HasPrefix(source, "oci://") {
						lastColon := strings.LastIndex(source, ":")
						if lastColon > 5 {
							tag := source[lastColon+1:]
							urlWithoutTag := source[:lastColon]
							if tag != "" {
								if version != "" && version != tag {
									return fmt.Errorf("version flag '%s' conflicts with tag '%s' in source URL", version, tag)
								}
								if version == "" {
									src.Version = tag
								}
								source = urlWithoutTag
							}
						}
					}
					src.URL = source
				} else {
					src.LocalPath = source
				}

				result, err := introspect.IntrospectHelmValues(ctx, src, opts)
				if err != nil {
					return err
				}

				addResult("", result)
				report.ComponentOrder = append(report.ComponentOrder, "")
			}

			// Resolve absolute paths if requested OR if interactive (needed for editor)
			if absolute || interactive {
				for _, res := range report.Components {
					// Ensure source is absolute
					absSource, err := filepath.Abs(res.Source)
					if err != nil {
						l.Warn(fmt.Sprintf("Failed to resolve absolute path for %s: %s", res.Source, err))
						absSource = res.Source
					}
					res.Source = absSource

					for i := range res.Values {
						for j := range res.Values[i].Locations {
							res.Values[i].Locations[j].File = filepath.Join(absSource, res.Values[i].Locations[j].File)
						}
					}

					// Resolve absolute paths for ValuesFiles
					for i := range res.ValuesFiles {
						if !filepath.IsAbs(res.ValuesFiles[i]) {
							res.ValuesFiles[i] = filepath.Join(absSource, res.ValuesFiles[i])
						}
					}

					// Resolve absolute paths for ZarfVars locations
					for i := range res.ZarfVars {
						for j := range res.ZarfVars[i].Locations {
							res.ZarfVars[i].Locations[j].File = filepath.Join(absSource, res.ZarfVars[i].Locations[j].File)
						}
					}
				}
			}

			if interactive {
				p := tea.NewProgram(tui.NewModel(report), tea.WithAltScreen())
				_, err := p.Run()
				return err
			}

			// OUTPUT GENERATION
			switch outputFormat {
			case "json":
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			case "yaml":
				return yaml.NewEncoder(out).Encode(report)
			case "table":
				if len(report.Variables) > 0 {
					fmt.Fprintln(out, "\n=== Zarf Package Variables ===")
					header := []string{"Name", "Default", "Description"}
					var data [][]string
					for _, v := range report.Variables {
						data = append(data, []string{
							v.Name,
							v.Default,
							v.Description,
						})
					}
					message.TableWithWriter(out, header, data)
					fmt.Fprintln(out, "")
				}

				// Sort keys using the tracked chronological component order
				keys := report.ComponentOrder

				for _, k := range keys {
					res := report.Components[k]
					if k != "" {
						fmt.Fprintf(out, "\n=== Component/Chart: %s ===\n", k)
					}
					if err := printIntrospectResult(out, res, "table"); err != nil {
						return err
					}
				}
				return nil
			default:
				return fmt.Errorf("unknown output format: %s", outputFormat)
			}
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version of the chart to fetch (if using URL)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	cmd.Flags().BoolVar(&includeSubcharts, "include-subcharts", true, "Include subcharts in the analysis")
	cmd.Flags().StringVar(&sortMode, "sort-mode", "path", "Sort mode (path, discovery)")
	cmd.Flags().StringVarP(&architecture, "architecture", "a", "", "Architecture for OCI images and Zarf packages")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "Force re-download of a package, ignoring cache")
	cmd.Flags().BoolVar(&wildcards, "wildcards", false, "Include wildcard/dynamic values in output")
	cmd.Flags().BoolVar(&absolute, "absolute", false, "Output absolute filepaths for locations")
	cmd.Flags().BoolVar(&noValues, "no-values", false, "Disable inference of nested paths from values.yaml")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Enter interactive TUI mode")

	return cmd
}

func printIntrospectResult(out io.Writer, result *introspect.IntrospectResult, format string) error {
	switch format {
	case "table":
		fmt.Fprintf(out, "Chart: %s (%s)\n", result.ChartName, result.ChartVersion)
		fmt.Fprintf(out, "Source: %s\n", result.Source)
		if result.RemoteSource != "" {
			fmt.Fprintf(out, "Remote Source: %s\n", result.RemoteSource)
		}

		// Calculate distinct subcharts
		subchartsMap := make(map[string]bool)
		for _, v := range result.Values {
			if v.Chart != result.ChartName {
				subchartsMap[v.Chart] = true
			}
		}
		var subcharts []string
		for k := range subchartsMap {
			subcharts = append(subcharts, k)
		}
		sort.Strings(subcharts)

		if len(subcharts) > 0 {
			fmt.Fprintf(out, "Subcharts: %s\n", strings.Join(subcharts, ", "))
		}

		if len(result.ValuesFiles) > 0 {
			// Always display absolute paths for values files in the header
			absSource, err := filepath.Abs(result.Source)
			if err != nil {
				// Fallback if abs fails
				absSource = result.Source
			}

			var absFiles []string
			for _, f := range result.ValuesFiles {
				absFiles = append(absFiles, filepath.Join(absSource, f))
			}
			fmt.Fprintf(out, "Values Files: %s\n", strings.Join(absFiles, ", "))
		}

		// Print Zarf Variables if any
		if len(result.ZarfVars) > 0 {
			fmt.Fprintln(out, "\nZarf Variables:")
			zHeader := []string{"Variable", "Default", "Location"}
			var zData [][]string

			// Group by variable to mimic the "Location" column stacking style if we want,
			// or just list them. The user asked for "same format as the values table".
			// The values table lists Path, Default, Type, Location.
			// Multiple locations for same path are stacked with empty cells for Path/Default.

			// We need to sort by Name first
			sort.Slice(result.ZarfVars, func(i, j int) bool {
				return result.ZarfVars[i].Name < result.ZarfVars[j].Name
			})

			for _, zv := range result.ZarfVars {
				def := introspect.FormatDefaultValue(zv.DefaultValue)

				for i, loc := range zv.Locations {
					rowName := zv.Name
					rowDef := def

					if i > 0 {
						rowName = ""
						rowDef = ""
					}

					zData = append(zData, []string{
						rowName,
						rowDef,
						fmt.Sprintf("%s:%d:%d", loc.File, loc.Line, loc.Column),
					})
				}
			}
			message.TableWithWriter(out, zHeader, zData)
			fmt.Fprintln(out, "")
		}

		header := []string{"Path", "Default", "Type", "Location"}
		var data [][]string

		for _, v := range result.Values {
			path := v.Path

			if len(v.Locations) == 0 {
				data = append(data, []string{
					path,
					introspect.FormatDefaultValue(v.DefaultValue),
					"inferred",
					"(inferred from values.yaml)",
				})
			} else {
				for i, loc := range v.Locations {
					rowPath := path
					if i > 0 {
						rowPath = ""
					}

					// Format default value for display (only on first occurrence)
					defVal := ""
					if i == 0 {
						defVal = introspect.FormatDefaultValue(v.DefaultValue)
					}

					data = append(data, []string{
						rowPath,
						defVal,
						loc.Kind,
						fmt.Sprintf("%s:%d:%d", loc.File, loc.Line, loc.Column),
					})
				}
			}
		}

		if len(data) == 0 {
			fmt.Fprintln(out, "\nNo values discovered.")
		} else {
			message.TableWithWriter(out, header, data)
		}

		return nil
	default:
		return fmt.Errorf("unknown output format: %s", format)
	}
}
