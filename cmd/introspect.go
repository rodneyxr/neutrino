package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/defenseunicorns/pkg/oci"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/rodneyxr/neutrino/pkg/helm/introspect"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/pkg/logger"
	"github.com/zarf-dev/zarf/src/pkg/message"
)

func init() {
	rootCmd.AddCommand(newIntrospectCmd(os.Stdout))
}

func newIntrospectCmd(out io.Writer) *cobra.Command {
	var (
		version          string
		outputFormat     string
		includeSubcharts bool
		architecture     string
	)

	cmd := &cobra.Command{
		Use:   "inspect [CHART_SOURCE]",
		Short: "Inspect a Helm chart to find overridable values",
		Long:  `Analyzes Helm chart templates to find which values from .Values are actually used.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Initialize logger
			l, err := logger.New(logger.ConfigDefault())
			if err != nil {
				return err
			}
			ctx := logger.WithContext(context.Background(), l)

			source := args[0]
			opts := introspect.IntrospectOptions{
				IncludeSubcharts: includeSubcharts,
			}

			// Determine if it's a Zarf package
			isZarf, err := isZarfPackage(source, architecture)
			if err != nil {
				return fmt.Errorf("failed to determine source type: %w", err)
			}

			if isZarf {
				pkgInspection, err := introspect.InspectZarfPackage(ctx, source, version, architecture, opts)
				if err != nil {
					return err
				}

				// Print Variables if present
				if len(pkgInspection.Variables) > 0 {
					fmt.Fprintln(out, "\n=== Zarf Package Variables ===")
					header := []string{"Name", "Default", "Description", "AutoIndent"}
					var data [][]string
					for _, v := range pkgInspection.Variables {
						data = append(data, []string{
							v.Name,
							v.Default,
							v.Description,
							fmt.Sprintf("%t", v.AutoIndent),
						})
					}
					message.TableWithWriter(out, header, data)
					fmt.Fprintln(out, "")
				}

				for key, res := range pkgInspection.ChartResults {
					fmt.Fprintf(out, "\n=== Component/Chart: %s ===\n", key)
					if err := printIntrospectResult(out, res, outputFormat); err != nil {
						return err
					}
				}
				return nil
			}

			// ... helm logic ...

			src := introspect.ChartSource{
				Version: version,
			}

			if isRemote(source) {
				src.URL = source
			} else {
				src.LocalPath = source
			}

			result, err := introspect.IntrospectHelmValues(src, opts)
			if err != nil {
				return err
			}

			return printIntrospectResult(out, result, outputFormat)
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version of the chart to fetch (if using URL)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	cmd.Flags().BoolVar(&includeSubcharts, "include-subcharts", true, "Include subcharts in the analysis")
	cmd.Flags().StringVarP(&architecture, "architecture", "a", "", "Architecture for OCI images and Zarf packages")

	return cmd
}

func isZarfPackage(source string, arch string) (bool, error) {
	if strings.HasSuffix(source, ".tar.zst") {
		return true, nil
	}
	if strings.HasPrefix(source, "oci://") {
		// potential zarf oci package
		ctx := context.TODO()

		targetArch := config.GetArch(arch)
		remote, err := oci.NewOrasRemote(source, oci.PlatformForArch(targetArch))
		if err != nil {
			return false, err
		}

		// Fetch root/manifest
		root, err := remote.FetchRoot(ctx)
		if err != nil {
			return false, err
		}

		if root.Config.MediaType == "application/vnd.zarf.config.v1+json" {
			return true, nil
		}
		return false, nil
	}
	return false, nil
}

func isRemote(source string) bool {
	return len(source) > 6 && (source[:6] == "oci://" || source[:7] == "http://" || source[:8] == "https://")
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

		header := []string{"Path", "Type", "Location"}
		var data [][]string

		for _, v := range result.Values {
			path := v.Path
			if v.Wildcard {
				path += " (dynamic)"
			}

			for i, loc := range v.Locations {
				rowPath := path
				rowType := loc.Kind
				if i > 0 {
					rowPath = ""
					rowType = ""
				}

				data = append(data, []string{
					rowPath,
					rowType,
					fmt.Sprintf("%s:%d", loc.File, loc.Line),
				})
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
