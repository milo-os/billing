// SPDX-License-Identifier: AGPL-3.0-only

// Package cmd contains the cobra command definitions for the billing binary.
package cmd

import "github.com/spf13/cobra"

// BuildInfo carries version metadata injected at build time via -ldflags.
type BuildInfo struct {
	Version      string
	GitCommit    string
	GitTreeState string
	BuildDate    string
}

// NewRootCommand returns the billing root cobra command.
// It has no RunE — invoking 'billing' with no subcommand prints help.
func NewRootCommand(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:   "billing",
		Short: "Datum Cloud billing service",
		// No RunE — 'billing' with no subcommand prints help.
	}
	root.AddCommand(newOperatorCommand(info))
	root.AddCommand(newGatewayCommand())
	return root
}
