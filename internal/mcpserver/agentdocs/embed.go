// SPDX-License-Identifier: AGPL-3.0-only

// Package agentdocs embeds the documents the assistant reads before calling
// billing's MCP tools: a knowledge document about usage, meters and spend,
// and skills (runbooks) with step-by-step procedures. It lives under
// internal/ so the container build, which copies internal/, includes it.
package agentdocs

import "embed"

const (
	// KnowledgeFile is the path of the knowledge document within FS.
	KnowledgeFile = "llms-full.txt"
	// SkillsDir holds the skills, each served at /runbooks/<name>.md.
	SkillsDir = "skills"
)

// FS holds the knowledge document and every skill.
//
//go:embed llms-full.txt skills/*.md
var FS embed.FS
