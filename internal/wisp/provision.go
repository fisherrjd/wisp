package wisp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The built-in provisioner: what wisp does to build a worktree when no script is in effect.
//
// It is the generic core of the provision-worktree.sh that wisp's author had been running for a
// year, moved into Go so that a fresh clone of any workspace can build a checkout with nothing
// configured and nothing accepted. git is the only requirement. Everything past `git worktree
// add` is a layer that applies only when the checkout asks for it and the tool is on PATH: an
// .envrc with direnv installed, a lockfile with its package manager installed and `install:` on.
// A machine with git and nothing else gets a complete worktree and a log line per layer skipped.
//
// One deliberate difference from the script it replaces. The script ran under the accept gate,
// so `direnv allow` on the new worktree was a step somebody had read. This runs ungated, so it
// only allows an .envrc whose copy in the checkout is already allowed: it extends consent the
// user gave, and never grants any.

// provisionJob is one checkout to build. Worktree is where wisp will look afterwards, computed
// by WorktreeFor and never derived here, so the check-not-trust after this is about one path.
type provisionJob struct {
	RepoDir  string // absolute: <workspace>/<repo>
	Worktree string // absolute
	Branch   string
	Base     string // "" means origin's default branch, freshly fetched
}

// envFiles are the per-checkout files a toolchain reads and git ignores, copied into a new
// worktree when the checkout has them. A generic list rather than a nix one: each is copied only
// if present, and none is required. .env is deliberately absent, since it holds secrets and a
// worktree should not multiply them.
var envFiles = []string{
	".envrc", "default.nix", "shell.nix",
	".tool-versions", ".nvmrc", ".python-version",
	filepath.Join(".claude", "settings.local.json"),
}

// provisionWorktree builds one checkout. Every step reports through log, the way the script's
// stdout landed in the provision window, and an error is what the script would have exited
// non-zero for.
func (c Config) provisionWorktree(job provisionJob, log func(string)) error {
	repo := filepath.Base(job.RepoDir)
	if !isDir(filepath.Join(job.RepoDir, ".git")) && !exists(filepath.Join(job.RepoDir, ".git")) {
		return fmt.Errorf("%s is not a git checkout", repo)
	}
	if exists(job.Worktree) {
		return fmt.Errorf("%s already exists and is not a directory wisp made", shortPath(job.Worktree))
	}

	base, err := resolveBase(job, log)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(job.Worktree), 0o755); err != nil {
		return err
	}

	attached := false
	if _, err := gitOut(job.RepoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+job.Branch); err == nil {
		// The branch outlived its worktree. Checked out as it is: a base is a start point for a
		// new branch, and applying it here would discard the commits already on this one.
		log(fmt.Sprintf("attaching existing branch %s (base ignored)", job.Branch))
		if err := gitRun(job.RepoDir, "worktree", "add", job.Worktree, job.Branch); err != nil {
			return err
		}
		attached = true
	} else {
		if err := gitRun(job.RepoDir, "worktree", "add", job.Worktree, "-b", job.Branch, base); err != nil {
			return err
		}
	}

	copyEnvFiles(job.RepoDir, job.Worktree, log)
	env := c.direnvFor(job.RepoDir, job.Worktree, log)
	if err := c.installDeps(job.Worktree, env, log); err != nil {
		return err
	}

	head, _ := gitOut(job.Worktree, "rev-parse", "--short", "HEAD")
	if attached {
		log(fmt.Sprintf("READY: %s (attached existing branch %s @ %s)", shortPath(job.Worktree), job.Branch, head))
	} else {
		log(fmt.Sprintf("READY: %s (branch %s, from %s @ %s)", shortPath(job.Worktree), job.Branch, base, head))
	}
	return nil
}

// resolveBase is the ref a new branch starts from: the manifest's base when it names one, and
// otherwise origin's default branch, freshly fetched. Not the checkout's HEAD, which is whatever
// branch that checkout happens to be parked on, and basing new work on somebody's half-finished
// feature branch is a rebase to discover later.
func resolveBase(job provisionJob, log func(string)) (string, error) {
	repo := filepath.Base(job.RepoDir)
	base := job.Base
	if base == "" {
		if _, err := gitOut(job.RepoDir, "remote", "get-url", "origin"); err != nil {
			return "", fmt.Errorf("%s has no origin remote and the item names no base; add `base:` to its orchestration.md", repo)
		}
		log("fetching origin")
		if err := gitRun(job.RepoDir, "fetch", "--quiet", "origin"); err != nil {
			return "", fmt.Errorf("%s: fetch failed: %v", repo, err)
		}
		// origin/HEAD is a local symref that is often missing or stale; asking the remote refreshes it.
		_, _ = gitOut(job.RepoDir, "remote", "set-head", "origin", "-a")
		def := ""
		if ref, err := gitOut(job.RepoDir, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
			def = strings.TrimPrefix(ref, "refs/remotes/origin/")
		}
		if def == "" {
			for _, cand := range []string{"main", "master"} {
				if _, err := gitOut(job.RepoDir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+cand); err == nil {
					def = cand
					break
				}
			}
		}
		if def == "" {
			return "", fmt.Errorf("could not tell origin's default branch for %s; add `base:` to the item's orchestration.md", repo)
		}
		base = "origin/" + def
	}
	if _, err := gitOut(job.RepoDir, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		return "", fmt.Errorf("base %s does not resolve in %s", base, repo)
	}
	sha, _ := gitOut(job.RepoDir, "rev-parse", "--short", base)
	log(fmt.Sprintf("branching from %s (%s)", base, sha))
	return base, nil
}

// gitOut runs a git query and returns its trimmed stdout.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if line := lastLine(errb.String()); line != "" {
			return "", errors.New(line)
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// gitRun runs a git command whose output belongs on screen: fetch progress, worktree add's
// "Preparing worktree" line. Both streams go to stderr, where the provision window shows them.
func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %v", args[0], err)
	}
	return nil
}

// copyEnvFiles brings the checkout's untracked toolchain files along. Worktrees hold tracked
// files only, and a checkout that has been working has usually accumulated the odd untracked
// file its shell depends on.
func copyEnvFiles(repoDir, wt string, log func(string)) {
	var copied []string
	for _, rel := range envFiles {
		src, dst := filepath.Join(repoDir, rel), filepath.Join(wt, rel)
		info, err := os.Stat(src)
		if err != nil || !info.Mode().IsRegular() || exists(dst) {
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(dst, raw, info.Mode().Perm()); err != nil {
			continue
		}
		copied = append(copied, rel)
	}
	if len(copied) > 0 {
		log("copied " + strings.Join(copied, ", ") + " from the checkout")
	}
}

// envRunner runs a command inside the worktree, through direnv when the worktree's .envrc is
// allowed and plainly otherwise.
type envRunner struct {
	dir    string
	direnv bool
}

func (r envRunner) cmd(name string, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if r.direnv {
		cmd = exec.Command("direnv", append([]string{"exec", ".", name}, args...)...)
		cmd.Env = append(os.Environ(), "DIRENV_LOG_FORMAT=")
	} else {
		cmd = exec.Command(name, args...)
	}
	cmd.Dir = r.dir
	return cmd
}

// has reports whether a tool is reachable from inside the environment, which under direnv may be
// true when it is not on the outer PATH.
func (r envRunner) has(tool string) bool {
	cmd := r.cmd("sh", "-c", "command -v "+tool)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run() == nil
}

// direnvFor allows and warms the worktree's .envrc when it may, and returns the runner every
// later step goes through. It may when direnv is installed, the worktree has an .envrc, and the
// checkout's own copy is already allowed: consent extended, never granted.
func (c Config) direnvFor(repoDir, wt string, log func(string)) envRunner {
	plain := envRunner{dir: wt}
	if !exists(filepath.Join(wt, ".envrc")) {
		return plain
	}
	if _, err := exec.LookPath("direnv"); err != nil {
		log("direnv is not on PATH, so .envrc is left for you")
		return plain
	}
	if !direnvAllowed(repoDir) {
		log(fmt.Sprintf("%s/.envrc is not allowed in the checkout, so it is not allowed in the worktree either; run `direnv allow` there", filepath.Base(repoDir)))
		return plain
	}
	if err := exec.Command("direnv", "allow", wt).Run(); err != nil {
		log("direnv allow failed: " + err.Error())
		return plain
	}
	log("direnv: allowed, warming the environment")
	warm := exec.Command("direnv", "exec", ".", "true")
	warm.Dir = wt
	warm.Env = append(os.Environ(), "DIRENV_LOG_FORMAT=")
	warm.Stdout, warm.Stderr = os.Stderr, os.Stderr
	if err := warm.Run(); err != nil {
		log("direnv: the environment did not load cleanly: " + err.Error())
	}
	return envRunner{dir: wt, direnv: true}
}

// direnvAllowed asks direnv whether a directory's .envrc has been allowed. The JSON form when this
// direnv has it, the text form otherwise, and "could not tell" is "no".
func direnvAllowed(dir string) bool {
	cmd := exec.Command("direnv", "status", "--json")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		var st struct {
			State struct {
				FoundRC struct {
					Allowed int    `json:"allowed"`
					Path    string `json:"path"`
				} `json:"foundRC"`
			} `json:"state"`
		}
		if json.Unmarshal(out, &st) == nil && st.State.FoundRC.Path != "" {
			return st.State.FoundRC.Allowed == 0
		}
	}
	cmd = exec.Command("direnv", "status")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Found RC allowed") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "Found RC allowed"))
			return v == "true" || v == "0"
		}
	}
	return false
}

// installDeps installs by whichever lockfile the worktree has, when `install:` is on. Each
// package manager is optional and logs its absence; a failed install is an error, as it was in
// the script, because a worktree that claims to be ready and cannot build is the worse outcome.
func (c Config) installDeps(wt string, env envRunner, log func(string)) error {
	if !c.Install {
		log("skipping dependency install (install: false); the toolchain is still set up")
		return nil
	}
	run := func(name string, args ...string) error {
		cmd := env.cmd(name, args...)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %v", name, strings.Join(args, " "), err)
		}
		return nil
	}
	switch {
	case exists(filepath.Join(wt, "pnpm-lock.yaml")):
		if !env.has("pnpm") {
			log("pnpm-lock.yaml present but pnpm is not installed; skipping install")
			return nil
		}
		log("pnpm install --frozen-lockfile")
		return run("pnpm", "install", "--frozen-lockfile")
	case exists(filepath.Join(wt, "yarn.lock")):
		var yarn []string
		switch {
		case env.has("yarn"):
			yarn = []string{"yarn"}
		case env.has("corepack"):
			log("yarn not on PATH, using corepack")
			yarn = []string{"corepack", "yarn"}
		default:
			log("yarn.lock present but neither yarn nor corepack is installed; skipping install")
			return nil
		}
		log(strings.Join(yarn, " ") + " install --immutable")
		if err := run(yarn[0], append(yarn[1:], "install", "--immutable")...); err != nil {
			return run(yarn[0], append(yarn[1:], "install", "--frozen-lockfile")...)
		}
		return nil
	case exists(filepath.Join(wt, "package-lock.json")):
		if !env.has("npm") {
			log("package-lock.json present but npm is not installed; skipping install")
			return nil
		}
		log("npm ci")
		if err := run("npm", "ci"); err != nil {
			return err
		}
		fixRollupNative(env, log)
		return nil
	case exists(filepath.Join(wt, "uv.lock")):
		if !env.has("uv") {
			log("uv.lock present but uv is not installed; skipping install")
			return nil
		}
		log("uv sync --frozen")
		return run("uv", "sync", "--frozen")
	}
	log("no lockfile wisp knows, nothing to install")
	return nil
}

// fixRollupNative works around npm bug #4828: a lockfile records only the optional dependencies
// resolved on the machine that wrote it, and `npm ci` installs exactly that set. Rollup ships its
// native binary as one optional dependency per platform, so a fresh worktree can land without the
// one it needs. The missing package is fetched on its own rather than through `npm install`, which
// would re-resolve the whole tree. A warning, never an error: the worktree is usable for everything
// but a build.
func fixRollupNative(env envRunner, log func(string)) {
	if !env.has("node") {
		return
	}
	nodeP := func(expr string) string {
		out, err := env.cmd("node", "-p", expr).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	ver := nodeP("require('rollup/package.json').version")
	if ver == "" {
		return
	}
	pkg := nodeP("process.platform === 'linux' ? '@rollup/rollup-linux-' + process.arch + '-gnu' : '@rollup/rollup-' + process.platform + '-' + process.arch")
	if pkg == "" {
		return
	}
	if env.cmd("node", "-e", "require('"+pkg+"')").Run() == nil {
		return
	}
	log(fmt.Sprintf("npm#4828: installing missing %s@%s", pkg, ver))
	if err := env.cmd("npm", "install", "--no-save", "--no-package-lock", pkg+"@"+ver).Run(); err != nil {
		log(fmt.Sprintf("WARNING: could not install %s@%s; `npm run build` may fail in this worktree", pkg, ver))
	}
}
