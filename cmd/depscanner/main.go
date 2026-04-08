// main.go is the CLI entrypoint.
// It registers all subcommands and delegates execution to cobra.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.0.1"

var (
	cfgPath  string
	cacheDir string
	format   string
	noFetch  bool
	branch   string
	packages bool
	funcName string

	rootCmd = &cobra.Command{
		Use:     "depscanner",
		Short:   "depscanner — scan Go repos for shared library usage",
		Long:    "depscanner scans Gitea org repositories to detect shared library dependencies and function call sites.",
		Version: version,
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default: $HOME/.depscanner.yaml)")
	rootCmd.PersistentFlags().StringVar(&cacheDir, "cache-dir", "", "local repo cache directory (overrides config)")
	rootCmd.PersistentFlags().StringVar(&format, "format", "table", "output format: table | json")
	rootCmd.PersistentFlags().BoolVar(&noFetch, "no-fetch", false, "skip git fetch, use cached repos only")

	rootCmd.AddCommand(newScanCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
