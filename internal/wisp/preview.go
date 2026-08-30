package wisp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if entries, err := c.Manifest(item); err == nil && len(entries) > 0 {
		b.WriteString("repos\n\n")
		for _, e := range entries {
			mark := "needs provisioning"
			if isDir(c.WorktreeFor(e.Repo, item)) {
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
		b.WriteString("no folder yet, this is a GitLab item\n")
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
