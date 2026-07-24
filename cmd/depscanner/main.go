// main.go is the CLI entrypoint.
// It registers all subcommands and delegates execution to cobra.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "1.1.0"

var formatter OutputFormatter

var (
	cfgPath     string
	cacheDir    string
	format      string
	noFetch     bool
	branch      string
	sourceFlag  string
	packages    bool
	funcNames   []string
	methodNames []string
	typeNames   []string
	constNames  []string
	varNames    []string
	check       bool

	rootCmd = &cobra.Command{
		Use:     "depscanner",
		Short:   "depscanner — scan Go repos for shared library usage",
		Long:    "depscanner scans Gitea org repositories to detect shared library dependencies and function call sites.",
		Version: version,
	}
)

func init() {
	formatter = NewOutputFormatter()

	rootCmd.PersistentFlags().
		StringVar(&cfgPath, "config", "", "config file (default: ./.depscanner.yaml, fallback $HOME/.depscanner.yaml)")
	rootCmd.PersistentFlags().StringVar(&cacheDir, "cache-dir", "", "local repo cache directory (overrides config)")
	rootCmd.PersistentFlags().StringVar(&format, "format", "table", "output format: table | json")
	rootCmd.PersistentFlags().BoolVar(&noFetch, "no-fetch", false, "skip git fetch, use cached repos only")
	rootCmd.PersistentFlags().StringVar(&branch, "branch", "", "scan repositories on a specific branch")

	rootCmd.AddCommand(newScanCmd())

	newInit := newInitCmd()
	rootCmd.AddCommand(newInit)

	newDiff := newDiffCmd()
	newDiff.Flags().
		StringVar(&sourceFlag, "source", "", "source module to analyze (required when multiple sources are configured)")
	rootCmd.AddCommand(newDiff)

	newImpact := newImpactCmd()
	newImpact.Flags().
		StringVar(&sourceFlag, "source", "", "source module to analyze (required when multiple sources are configured)")
	rootCmd.AddCommand(newImpact)

	rootCmd.AddCommand(newUnshallowCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func test() {
	type SystemRoleGlobal string
	type SystemRoleGroup string
	type SystemRoleAttribute string
	type TransactionRole string
	type TransactionRoleType string

	const (
		SystemRoleGlobalAssetAttribute SystemRoleGlobal = "assetAttribute"

		SystemRoleGroupManageRole SystemRoleGroup = "manageRole"

		SystemRoleAttributeCreate SystemRoleAttribute = "create"
		SystemRoleAttributeView   SystemRoleAttribute = "view" // read
		SystemRoleAttributeEdit   SystemRoleAttribute = "edit" // update
		SystemRoleAttributeDelete SystemRoleAttribute = "delete"

		TransactionRoleBorrowing TransactionRole = "borrowingRole"

		TransactionRoleTypeManager TransactionRoleType = "manager"
	)

	type User struct {
		ID          string
		FullName    string
		Email       string
		CompanyCode string
		SystemRole  struct {
			GlobalRole map[SystemRoleGlobal]map[SystemRoleAttribute]bool
			GroupRoles map[SystemRoleGroup][]string // group ids
		}
		TransactionRole map[TransactionRole]map[TransactionRoleType][]string // group ids
	}
}
