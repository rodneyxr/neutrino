package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "neutrino",
	Short: "Neutrino is a CLI tool for inspecting Zarf packages and Helm charts",
	Long:  `Neutrino provides tools for static analysis and introspection of Helm charts and Zarf packages.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
