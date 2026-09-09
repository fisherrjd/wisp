package wisp

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
)

// The bundles wisp ships, compiled into the binary. A directory each, exactly the shape a bundle
// on disk has, so `wisp workflow init --from <name>` is a copy and nothing else.
//
// None of them carries a script, and a test holds that line. That is what makes "a shipped
// bundle is not gated" honest: there is nothing in one that could execute out of the binary, and
// a workspace-relative `provision:` one of them names still passes through the script rule like
// every other path inside the workspace.
//
//go:embed bundles
var shippedFS embed.FS

// shippedNames is the listing order. `default` is the built-in and stays first; it is the
// workspace workflow under wisp's own name. `minimal` is the floor with no seed.
var shippedNames = []string{"default", "workspace", "scratch", "minimal"}

func isShipped(name string) bool { return slices.Contains(shippedNames, name) }

// ShippedManifest is the workflow.yaml of a shipped bundle, or nil.
func ShippedManifest(name string) []byte {
	if !isShipped(name) {
		return nil
	}
	raw, err := shippedFS.ReadFile(path.Join("bundles", name, WorkflowFile))
	if err != nil {
		return nil
	}
	return raw
}

// shippedBundle parses a shipped bundle. Dir is empty, since it lives nowhere on disk, and a
// seed directory comes back as an fs.FS over the embedded tree rather than as a path.
func shippedBundle(name string) (Workflow, bool) {
	raw := ShippedManifest(name)
	if raw == nil {
		return Workflow{}, false
	}
	w, err := parseWorkflow("", raw)
	if err != nil {
		return Workflow{}, false
	}
	w.Dir = ""
	if w.Item.Seed != "" {
		if sub, err := fs.Sub(shippedFS, path.Join("bundles", name, filepath.ToSlash(w.Item.Seed))); err == nil {
			w.SeedFS = sub
		}
	}
	return w, true
}

// copyShipped writes a shipped bundle's whole tree under dir, which must not exist yet.
func copyShipped(name, dir string) error {
	root := path.Join("bundles", name)
	return fs.WalkDir(shippedFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.FromSlash(p))
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := shippedFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
}
