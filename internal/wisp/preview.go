package wisp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Preview is what the right-hand pane shows for the highlighted item.
//
// For a live session it is the agent's own pane, so you can see what it is doing before
// switching to it. For everything else it is the manifest (the most useful thing to know before
// committing to an open) followed by the item's notes.
func (c Config) Preview(item Item, width int) string {
	// Attached from here, so the pane is already rendering whatever the agent is doing, whether
	// that agent is on this machine or another one.
	if session := c.FindSession(item.Name); session != "" {
		return CapturePane(session, false)
	}
	if c.IsRemote() {
		out, err := c.Location.Preview(item.Name, width)
		if err != nil {
			return err.Error()
		}
		return out
	}

	var b strings.Builder
	w := c.WorkflowFor(item, "")
	entries, err := c.Manifest(w, item)
	// The workflow's own preview, when it has one, bounded tightly because this runs on every
	// cursor move. Anything it will not or cannot say falls through to the summary below.
	if w.Hooks.Preview != "" && err == nil {
		if body, ok := c.runPreviewHook(w, item, entries, width); ok {
			return body
		}
	}
	if err == nil && len(entries) > 0 {
		b.WriteString("repos\n\n")
		for _, e := range entries {
			mark := "needs provisioning"
			if isDir(c.WorktreeFor(w, e.Repo, item)) {
				mark = "worktree ready"
			}
			fmt.Fprintf(&b, "  %s\n    %s  %s\n", e.Repo, e.Branch, mark)
		}
		b.WriteString("\n")
	} else if err != nil {
		fmt.Fprintf(&b, "manifest error: %v\n\n", err)
	}

	dir := c.ItemDir(item.Name)
	switch {
	case exists(filepath.Join(dir, "notes.md")):
		raw, _ := os.ReadFile(filepath.Join(dir, "notes.md"))
		b.Write(StripFrontmatter(raw))
	case isDir(dir):
		if alt := firstMarkdown(dir); alt != "" {
			fmt.Fprintf(&b, "%s\n\n", filepath.Base(alt))
			raw, _ := os.ReadFile(alt)
			b.Write(StripFrontmatter(raw))
		} else {
			b.WriteString("folder exists, no markdown yet\n")
		}
	default:
		fmt.Fprintf(&b, "no folder yet, this is a %s item\n", c.RemoteLabel())
		if item.Title != "" {
			fmt.Fprintf(&b, "\n%s\n", item.Title)
		}
	}
	return b.String()
}

func firstMarkdown(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// previewTimeout bounds the preview hook. Five seconds, because the pane repaints on every cursor
// move and a minute would be a picker that stops answering.
const previewTimeout = 5 * time.Second

// previewInput is the context input plus what the list knows about the row.
type previewInput struct {
	contextInput
	State string `json:"state"`
	Title string `json:"title"`
	Width int    `json:"width"`
}

// runPreviewHook asks the workflow for the pane body. Silent on failure: this runs on every
// keystroke, and a status line per move would be noise where a blank fallback is not.
func (c Config) runPreviewHook(w Workflow, item Item, entries []Entry, width int) (string, bool) {
	state := "folder"
	if !isDir(c.ItemDir(item.Name)) {
		state = "remote"
	}
	payload, err := json.Marshal(previewInput{contextInput: c.hookInput(w, item, entries), State: state, Title: item.Title, Width: width})
	if err != nil {
		return "", false
	}
	out, err := c.runHookFor(previewTimeout, w.Hooks.Preview, payload, item.Name)
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "", false
	}
	return string(out), true
}
