// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"io"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"
)

// newRootCommand builds `datumctl billing`. plugin.NewRootCmd adds the
// --org, --project and --output flags, pre-filled from the datumctl context,
// for every subcommand.
func newRootCommand(out io.Writer, newAPI func() (api, error)) *cobra.Command {
	root := plugin.NewRootCmd(pluginName, pluginDescription)
	root.Long = `Work with billing on Datum Cloud: metered usage and spend for your
organization or project, and the meters usage is reported for.`
	root.Example = `  # Usage and spend for the current billing cycle
  datumctl billing usage

  # The meters usage is reported for
  datumctl billing meters`
	root.Args = cobra.NoArgs
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetOut(out)

	root.AddCommand(newUsageCommand(out, newAPI), newMetersCommand(out, newAPI))
	return root
}
