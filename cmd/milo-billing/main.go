// SPDX-License-Identifier: AGPL-3.0-only

// Command milo-billing is the billing plugin for datumctl. `datumctl billing
// usage` reads metered usage and spend from usage.billing.miloapis.com
// UsageQuery with the user's own datumctl session, in the context of the
// active organization or project.
package main

import (
	"errors"
	"fmt"
	"os"

	"go.datum.net/datumctl/plugin"
)

func main() {
	// Serve --plugin-manifest before cobra runs, so the manifest is emitted even
	// if flag parsing would fail.
	plugin.ServeManifest(pluginManifest())

	root := newRootCommand(os.Stdout, newDatumClient)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		var usageErr *usageError
		if errors.As(err, &usageErr) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// usageError is a mistake in how the command was invoked (exit code 2).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}
