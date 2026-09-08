package wisp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// A workflow does not cross a host boundary on its own.
//
// A remote workspace runs its own wisp, its own Load and its own .wisp.yaml, and nothing is
// shipped over ssh: that is the design, and it is what makes remote workspaces need no design of
// their own. The consequence is that a bundle in ~/.config/wisp/workflows here has no effect on a
// workspace living on another machine.
//
// So across a host boundary a workflow is necessarily a copy. There is no shared filesystem and
// the far side reads its own config. The question is not how to avoid the copy, it is whether
// wisp makes one for you and tells you when it has drifted.
//
// Deliberately not auto-sync on open: opening a remote item would then silently write into
// another machine's config directory, which is a surprising thing for a command called `open` to
// do, and it would fire on the non-interactive paths (board --json, the remote board fetches
// under BatchMode=yes) where nothing can ask you first.

// PushWorkflow copies one of your bundles to another machine's workflows directory.
//
// tar over the ssh connection wisp is already holding open rather than scp per file: a bundle is
// a handful of small files, and the round trips cost more than the bytes.
func (c Config) PushWorkflow(addr, host string) (string, error) {
	// Normalised once, here, and used for both ends. WorkflowDir trims and the remote shell line
	// did not, so `push '  solo  '` packed the right directory and created one on the far side
	// that nothing could address afterwards.
	addr = normalizeAddr(addr)
	if IsWorkspaceWorkflow(addr) {
		return "", fmt.Errorf("%s belongs to this workspace, so it travels with it; there is nothing to push", addr)
	}
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return "", err
	}
	if !isDir(dir) {
		return "", fmt.Errorf("no workflow %q at %s", addr, shortPath(dir))
	}
	if !exists(filepath.Join(dir, WorkflowFile)) {
		return "", fmt.Errorf("%s has no %s, so there is nothing to push", shortPath(dir), WorkflowFile)
	}
	files, size := bundleContents(dir)

	loc := c.hostLocation(host)
	// The far side's own config directory, expanded by its shell rather than guessed here: XDG
	// may be set over there and is none of this machine's business.
	remote := `"${XDG_CONFIG_HOME:-$HOME/.config}"/wisp/workflows`
	line := fmt.Sprintf("mkdir -p %s/%s && tar -xf - -C %s/%s", remote, shellQuote(addr), remote, shellQuote(addr))

	tar := exec.Command("tar", "-cf", "-", "-C", dir, ".")
	var archive, tarErr bytes.Buffer
	tar.Stdout, tar.Stderr = &archive, &tarErr
	if err := tar.Run(); err != nil {
		return "", fmt.Errorf("packing %s: %v", shortPath(dir), lastLine(tarErr.String()))
	}
	if _, err := loc.execIn(line, &archive); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s to %s:~/.config/wisp/workflows/%s  (%d files, %s)",
		addr, host, addr, files, humanBytes(size)), nil
}

// hostLocation resolves a configured machine name to its ssh target, falling back to the
// argument itself. `wisp host` is what the usage text tells you to name, so naming one of those
// has to work; an ssh target typed directly still does, because refusing it would be a rule
// nobody asked for.
func (c Config) hostLocation(host string) Location {
	if target, ok := c.Hosts[host]; ok && target != "" {
		return Location{Host: target}
	}
	return Location{Host: host}
}

// HostWorkflow is one workflow as it stands on another machine, next to how it stands here.
type HostWorkflow struct {
	Name  string
	State string // same, differs, missing, or only there
}

// HostWorkflows compares this machine's bundles with another's.
//
// The comparison is a hash of workflow.yaml, which is what names every script, so a change to it
// is the change worth reporting. Push is idempotent and this is what makes the drift it
// reintroduces visible rather than silent, which is the standard applied everywhere else here.
func (c Config) HostWorkflows(host string) ([]HostWorkflow, error) {
	loc := c.hostLocation(host)
	// One shell line rather than a wisp subcommand: the far side may be running an older wisp
	// that has never heard of workflows, and this still answers correctly there.
	line := `d="${XDG_CONFIG_HOME:-$HOME/.config}"/wisp/workflows; ` +
		`for f in "$d"/*/workflow.yaml; do [ -f "$f" ] || continue; ` +
		`n=$(basename "$(dirname "$f")"); ` +
		`h=$(shasum -a 256 "$f" 2>/dev/null || sha256sum "$f"); ` +
		`echo "$n ${h%% *}"; done`
	out, err := loc.exec(line)
	if err != nil {
		return nil, err
	}
	there := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if name, sum, ok := strings.Cut(strings.TrimSpace(l), " "); ok && name != "" {
			there[name] = sum
		}
	}

	seen := map[string]bool{}
	var rows []HostWorkflow
	for _, name := range dirNames(UserWorkflowsDir()) {
		seen[name] = true
		mine, err := WorkflowSum(filepath.Join(UserWorkflowsDir(), name))
		switch {
		case err != nil:
			continue // no manifest here, so there is nothing to compare
		case there[name] == "":
			rows = append(rows, HostWorkflow{Name: name, State: "missing"})
		case there[name] == mine:
			rows = append(rows, HostWorkflow{Name: name, State: "same"})
		default:
			rows = append(rows, HostWorkflow{Name: name, State: "differs"})
		}
	}
	for name := range there {
		if !seen[name] {
			rows = append(rows, HostWorkflow{Name: name, State: "only there"})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// bundleContents is what a bundle holds, for the line printed after a push. Counting beats
// listing here: a push that says "4 files" is checkable, and one that says nothing is not.
//
// Walked, not listed. tar ships the whole tree, and a bundle keeping its scripts in bin/, which
// is what the generated template suggests, reported "1 files" after moving four.
func bundleContents(dir string) (int, int64) {
	files, size := 0, int64(0)
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files++
		if fi, err := d.Info(); err == nil {
			size += fi.Size()
		}
		return nil
	})
	return files, size
}

// Stops at MB because that is where the things it measures stop: a bundle is scripts and a
// manifest, and the hook ceiling is 8 MB. It used to stop at kB, which printed that ceiling as
// "8192.0 kB" in the one message whose whole job is telling you what the limit was.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
