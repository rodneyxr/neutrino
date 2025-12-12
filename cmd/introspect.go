package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/defenseunicorns/pkg/oci"
	"github.com/goccy/go-yaml"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rodneyxr/neutrino/pkg/helm/introspect"
	"github.com/spf13/cobra"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/config"
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

			// Structure for final output
			type InspectionReport struct {
				Variables  []v1alpha1.InteractiveVariable          `json:"variables,omitempty" yaml:"variables,omitempty"`
				Components map[string]*introspect.IntrospectResult `json:"components" yaml:"components"`
			}

			report := InspectionReport{
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

			// Determine if it's a Zarf package
			isZarf, err := isZarfPackage(ctx, source, architecture)
			if err != nil {
				return fmt.Errorf("failed to determine source type: %w", err)
			}

			if isZarf {
				pkgInspection, err := introspect.InspectZarfPackage(ctx, source, version, architecture, opts)
				if err != nil {
					return err
				}

				report.Variables = pkgInspection.Variables

				// Fix: using explicit filtering loop instead of generic one
				keys := make([]string, 0, len(pkgInspection.ChartResults))
				for k := range pkgInspection.ChartResults {
					keys = append(keys, k)
				}
				sort.Strings(keys) // Stable order for output

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

				if isRemote(source) {
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
			}

			// Resolve absolute paths if requested
			if absolute {
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
				}
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

				// Sort keys for deterministic table output
				keys := make([]string, 0, len(report.Components))
				for k := range report.Components {
					keys = append(keys, k)
				}
				sort.Strings(keys)

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

	return cmd
}

func isZarfPackage(ctx context.Context, source string, arch string) (bool, error) {
	if strings.HasSuffix(source, ".tar.zst") {
		return true, nil
	}
	if strings.HasPrefix(source, "oci://") {
		// potential zarf oci package
		l := logger.From(ctx)

		targetArch := config.GetArch(arch)
		platform := oci.PlatformForArch(targetArch)

		// 1. Try with the target/default architecture
		isZarf, err := checkZarfRef(ctx, source, platform)
		if err == nil {
			return isZarf, nil
		}
		l.Debug(fmt.Sprintf("isZarfPackage: failed with target arch %s: %s", targetArch, err))

		// 2. Fallback: If target was not amd64, try amd64 as it's the most common for Zarf packages
		if platform.Architecture != "amd64" {
			amd64Platform := oci.PlatformForArch("amd64")
			isZarf, err := checkZarfRef(ctx, source, amd64Platform)
			if err == nil {
				if isZarf {
					l.Debug("isZarfPackage: detected Zarf package via amd64 fallback")
					return true, nil
				}
				// Found something on amd64 but it wasn't Zarf
				l.Debug("isZarfPackage: detected non-Zarf artifact via amd64 fallback")
				return false, nil
			}
			l.Debug(fmt.Sprintf("isZarfPackage: fallback arch (amd64) check failed: %s", err))
		}

		return false, nil
	}
	return false, nil
}

func checkZarfRef(ctx context.Context, source string, platform ocispec.Platform) (bool, error) {
	remote, err := oci.NewOrasRemote(source, platform)
	if err != nil {
		return false, err
	}
	root, err := remote.FetchRoot(ctx)
	if err != nil {
		return false, err
	}
	if root.Config.MediaType == "application/vnd.zarf.config.v1+json" {
		return true, nil
	}
	return false, nil
}

func isRemote(source string) bool {
	return len(source) > 6 && (source[:6] == "oci://" || source[:7] == "http://" || source[:8] == "https://")
}

func formatDefaultValue(val interface{}) string {
	if val == nil {
		return "-"
	}

	// Marshal to YAML in flow style (JSON-like with {} and [])
	yamlBytes, err := yaml.MarshalWithOptions(val, yaml.Flow(true))
	if err != nil {
		return fmt.Sprintf("%v", val)
	}

	result := strings.TrimSpace(string(yamlBytes))

	// Remove .0 from integers (e.g., "1.0" -> "1")
	// Match pattern: digit followed by .0 followed by non-digit or end of string
	result = strings.ReplaceAll(result, ".0,", ",")
	result = strings.ReplaceAll(result, ".0}", "}")
	result = strings.ReplaceAll(result, ".0]", "]")
	result = strings.TrimSuffix(result, ".0")

	// Truncate to 32 characters
	if len(result) > 32 {
		return result[:29] + "..."
	}
	return result
}

func printIntrospectResult(out io.Writer, result *introspect.IntrospectResult, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	case "yaml":
		return yaml.NewEncoder(out).Encode(result)
	case "table":
		fmt.Fprintf(out, "Chart: %s (%s)\n", result.ChartName, result.ChartVersion)
		fmt.Fprintf(out, "Source: %s\n", result.Source)

		header := []string{"Path", "Default", "Type", "Location"}
		var data [][]string

		for _, v := range result.Values {
			path := v.Path

			if len(v.Locations) == 0 {
				data = append(data, []string{
					path,
					formatDefaultValue(v.DefaultValue),
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
						defVal = formatDefaultValue(v.DefaultValue)
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
