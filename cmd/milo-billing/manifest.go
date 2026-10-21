// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"go.datum.net/datumctl/plugin"
)

const (
	pluginName        = "billing"
	pluginDescription = "View metered usage and spend for your organization or project"
	// pluginAPIVersion is the version of the datumctl <-> plugin contract this
	// binary speaks, not the usage API version.
	pluginAPIVersion = 1
	// minDatumctlVersion is the lowest datumctl that sets the DATUM_* plugin
	// context this binary reads.
	minDatumctlVersion = "0.5.0"
	minAPIVersion      = pluginAPIVersion
)

// pluginVersion defaults to "0.0.0" for an unreleased local build. goreleaser
// overrides it with the release tag via -ldflags "-X main.pluginVersion=...".
var pluginVersion = "0.0.0"

func pluginManifest() plugin.Manifest {
	return plugin.Manifest{
		Name:               pluginName,
		Version:            pluginVersion,
		Description:        pluginDescription,
		APIVersion:         pluginAPIVersion,
		MinDatumctlVersion: minDatumctlVersion,
		MinAPIVersion:      minAPIVersion,
	}
}
