// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"go.miloapis.com/billing/internal/mcpserver/agentdocs"
)

const (
	knowledgePath = "/llms-full.txt"
	runbookPrefix = "/runbooks/"
)

type document struct {
	body        []byte
	contentType string
}

// docsHandler serves the embedded knowledge document and skills.
type docsHandler map[string]document

func newDocsHandler() (docsHandler, error) {
	docs := docsHandler{}

	knowledge, err := agentdocs.FS.ReadFile(agentdocs.KnowledgeFile)
	if err != nil {
		return nil, fmt.Errorf("reading embedded knowledge: %w", err)
	}
	docs[knowledgePath] = document{body: knowledge, contentType: "text/plain; charset=utf-8"}

	entries, err := fs.ReadDir(agentdocs.FS, agentdocs.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("reading embedded skills: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body, err := agentdocs.FS.ReadFile(path.Join(agentdocs.SkillsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded skill %s: %w", entry.Name(), err)
		}
		docs[runbookPrefix+entry.Name()] = document{body: body, contentType: "text/markdown; charset=utf-8"}
	}
	return docs, nil
}

func (d docsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	doc, ok := d[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", doc.contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(doc.body)
}
