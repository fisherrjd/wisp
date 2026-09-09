package wisp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitFixture is a workspace holding one checkout of a bare origin whose default branch is main,
// with one commit pushed. PATH is cut down to git alone plus a directory the test may plant
// fakes in, so direnv, npm and friends are absent unless a test says otherwise: the built-in
// provisioner has to be complete with git and nothing else.
type gitFixture struct {
	t     *testing.T
	c     Config
	repo  string // the checkout, <workspace>/repo
	fakes string // on PATH, empty until a test writes into it
	logs  []string
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	fakes := t.TempDir()
	// System dirs stay for sh, mkdir and the like; the tools the provisioner treats as optional
	// live elsewhere on a real machine and are absent here.
	t.Setenv("PATH", fakes+":"+bin+":/usr/bin:/bin")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "wisp test")
	t.Setenv("GIT_AUTHOR_EMAIL", "wisp@test")
	t.Setenv("GIT_COMMITTER_NAME", "wisp test")
	t.Setenv("GIT_COMMITTER_EMAIL", "wisp@test")

	f := &gitFixture{t: t, fakes: fakes}
	f.c = Config{Workspace: t.TempDir(), Vault: "working_items", Worktrees: ".worktrees", Name: "probe", Accepted: map[string]string{}}
	origin := filepath.Join(t.TempDir(), "origin.git")
	f.git("", "init", "--bare", "-b", "main", origin)
	f.repo = filepath.Join(f.c.Workspace, "repo")
	f.git("", "clone", "-q", origin, f.repo)
	f.write("README.md", "hello\n")
	f.git(f.repo, "add", "README.md")
	f.git(f.repo, "commit", "-q", "-m", "root")
	f.git(f.repo, "push", "-q", "-u", "origin", "main")
	return f
}

func (f *gitFixture) git(dir string, args ...string) string {
	f.t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *gitFixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// fake plants an executable on PATH that appends its argv to a tally file and runs body.
func (f *gitFixture) fake(name, body string) string {
	f.t.Helper()
	tally := filepath.Join(f.fakes, name+".calls")
	script := "#!/bin/sh\necho \"$*\" >> " + tally + "\n" + body
	if err := os.WriteFile(filepath.Join(f.fakes, name), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return tally
}

func (f *gitFixture) log(s string) { f.logs = append(f.logs, s) }

func (f *gitFixture) logged(substr string) bool {
	for _, l := range f.logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func (f *gitFixture) job(branch, base string) provisionJob {
	slug := branch[strings.LastIndex(branch, "/")+1:]
	return provisionJob{RepoDir: f.repo, Worktree: filepath.Join(f.c.WorktreeRoot(), "repo--"+slug), Branch: branch, Base: base}
}

func TestGoProvisionerBranchesFromOriginsDefaultNotTheParkedHEAD(t *testing.T) {
	f := newGitFixture(t)
	mainSHA := f.git(f.repo, "rev-parse", "HEAD")
	// Park the checkout on a branch with work on it, the way a real checkout usually is.
	f.git(f.repo, "checkout", "-q", "-b", "wip")
	f.write("wip.txt", "half done\n")
	f.git(f.repo, "add", "wip.txt")
	f.git(f.repo, "commit", "-q", "-m", "wip")

	job := f.job("feature/1-x", "")
	if err := f.c.provisionWorktree(job, f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if got := f.git(job.Worktree, "rev-parse", "HEAD"); got != mainSHA {
		t.Errorf("worktree HEAD %s, want origin/main %s: it branched from the parked checkout", got, mainSHA)
	}
	if got := f.git(job.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature/1-x" {
		t.Errorf("branch = %s", got)
	}
	if !f.logged("branching from origin/main") || !f.logged("READY") {
		t.Errorf("logs: %v", f.logs)
	}
	// With only git on PATH, every optional layer is skipped and says so, and nothing failed.
	if !f.logged("skipping dependency install") {
		t.Errorf("install: false should be said: %v", f.logs)
	}
}

func TestGoProvisionerAttachesAnExistingBranchWithoutRebasing(t *testing.T) {
	f := newGitFixture(t)
	f.git(f.repo, "branch", "feature/1-x")
	f.git(f.repo, "checkout", "-q", "feature/1-x")
	f.write("work.txt", "done\n")
	f.git(f.repo, "add", "work.txt")
	f.git(f.repo, "commit", "-q", "-m", "work on it")
	want := f.git(f.repo, "rev-parse", "HEAD")
	f.git(f.repo, "checkout", "-q", "main")

	// A base is given and must be ignored: the branch already has commits.
	job := f.job("feature/1-x", "origin/main")
	if err := f.c.provisionWorktree(job, f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if got := f.git(job.Worktree, "rev-parse", "HEAD"); got != want {
		t.Errorf("attached worktree HEAD %s, want the branch's own %s", got, want)
	}
	if !f.logged("attaching existing branch") {
		t.Errorf("logs: %v", f.logs)
	}
}

func TestGoProvisionerHonoursTheManifestBase(t *testing.T) {
	f := newGitFixture(t)
	f.git(f.repo, "checkout", "-q", "-b", "release")
	f.write("rel.txt", "r\n")
	f.git(f.repo, "add", "rel.txt")
	f.git(f.repo, "commit", "-q", "-m", "release")
	rel := f.git(f.repo, "rev-parse", "HEAD")
	f.git(f.repo, "push", "-q", "origin", "release")
	f.git(f.repo, "checkout", "-q", "main")

	job := f.job("feature/2-y", "origin/release")
	if err := f.c.provisionWorktree(job, f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if got := f.git(job.Worktree, "rev-parse", "HEAD"); got != rel {
		t.Errorf("HEAD %s, want origin/release %s", got, rel)
	}

	bad := f.job("feature/3-z", "origin/nope")
	if err := f.c.provisionWorktree(bad, f.log); err == nil || !strings.Contains(err.Error(), "origin/nope") {
		t.Errorf("a base that does not resolve should be named: %v", err)
	}

	// No origin and no base: nothing to branch from, said in as many words.
	f.git(f.repo, "remote", "remove", "origin")
	none := f.job("feature/4-w", "")
	if err := f.c.provisionWorktree(none, f.log); err == nil || !strings.Contains(err.Error(), "no origin") {
		t.Errorf("want a no-origin error, got %v", err)
	}
}

func TestGoProvisionerCopiesUntrackedEnvFilesWithoutOverwriting(t *testing.T) {
	f := newGitFixture(t)
	// Tracked, so the worktree already has it: must not be replaced by the checkout's copy.
	f.write(".nvmrc", "tracked\n")
	f.git(f.repo, "add", ".nvmrc")
	f.git(f.repo, "commit", "-q", "-m", "nvmrc")
	f.git(f.repo, "push", "-q", "origin", "main")
	f.write(".nvmrc", "local edit\n")
	// Untracked: these come along.
	f.write("default.nix", "{ }\n")
	f.write(".envrc", "use nix\n")
	f.write(".claude/settings.local.json", "{}\n")
	// Never: secrets do not multiply.
	f.write(".env", "TOKEN=x\n")

	job := f.job("feature/1-x", "")
	if err := f.c.provisionWorktree(job, f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	read := func(rel string) string {
		raw, err := os.ReadFile(filepath.Join(job.Worktree, rel))
		if err != nil {
			return "<absent>"
		}
		return string(raw)
	}
	if read("default.nix") != "{ }\n" || read(".envrc") != "use nix\n" || read(filepath.Join(".claude", "settings.local.json")) != "{}\n" {
		t.Errorf("env files not copied: %q %q %q", read("default.nix"), read(".envrc"), read(".claude/settings.local.json"))
	}
	if read(".nvmrc") != "tracked\n" {
		t.Errorf("a tracked file was overwritten by the checkout's edit: %q", read(".nvmrc"))
	}
	if read(".env") != "<absent>" {
		t.Error(".env was copied into the worktree")
	}
	if !f.logged("direnv is not on PATH") {
		t.Errorf("an .envrc with no direnv should be said once: %v", f.logs)
	}
}

func TestGoProvisionerInstallsOnlyWhenAsked(t *testing.T) {
	f := newGitFixture(t)
	f.write("package-lock.json", "{}\n")
	f.git(f.repo, "add", "package-lock.json")
	f.git(f.repo, "commit", "-q", "-m", "lock")
	f.git(f.repo, "push", "-q", "origin", "main")
	calls := f.fake("npm", "exit 0\n")

	job := f.job("feature/1-x", "")
	if err := f.c.provisionWorktree(job, f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if exists(calls) {
		t.Error("npm ran with install: false")
	}

	f.c.Install = true
	job2 := f.job("feature/2-y", "")
	if err := f.c.provisionWorktree(job2, f.log); err != nil {
		t.Fatalf("provision with install: %v", err)
	}
	raw, _ := os.ReadFile(calls)
	if !strings.Contains(string(raw), "ci") {
		t.Errorf("npm ci did not run: %q", raw)
	}
	// No node on PATH, so the rollup fix has nothing to do and says nothing alarming.
	if f.logged("WARNING") {
		t.Errorf("a missing node should not warn: %v", f.logs)
	}

	// A lockfile whose tool is absent is a log line, not a failure.
	f.write("pnpm-lock.yaml", "lockfileVersion: 9\n")
	f.git(f.repo, "add", "pnpm-lock.yaml")
	f.git(f.repo, "commit", "-q", "-m", "pnpm")
	f.git(f.repo, "push", "-q", "origin", "main")
	if err := f.c.provisionWorktree(f.job("feature/3-z", ""), f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !f.logged("pnpm is not installed") {
		t.Errorf("logs: %v", f.logs)
	}
}

func TestGoProvisionerExtendsDirenvConsentButNeverGrantsIt(t *testing.T) {
	f := newGitFixture(t)
	f.write(".envrc", "export X=1\n")

	// Not allowed in the checkout: no allow in the worktree, and the reason is logged.
	calls := f.fake("direnv", "case \"$1\" in status) echo '{\"state\":{\"foundRC\":{\"allowed\":1,\"path\":\"x\"}}}';; esac\nexit 0\n")
	if err := f.c.provisionWorktree(f.job("feature/1-x", ""), f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	raw, _ := os.ReadFile(calls)
	if strings.Contains(string(raw), "allow") {
		t.Errorf("direnv allow ran for an .envrc the checkout had not allowed: %q", raw)
	}
	if !f.logged("not allowed in the checkout") {
		t.Errorf("logs: %v", f.logs)
	}

	// Allowed in the checkout: allowed and warmed in the worktree.
	calls = f.fake("direnv", "case \"$1\" in status) echo '{\"state\":{\"foundRC\":{\"allowed\":0,\"path\":\"x\"}}}';; esac\nexit 0\n")
	if err := f.c.provisionWorktree(f.job("feature/2-y", ""), f.log); err != nil {
		t.Fatalf("provision: %v", err)
	}
	raw, _ = os.ReadFile(calls)
	if !strings.Contains(string(raw), "allow ") || !strings.Contains(string(raw), "exec . true") {
		t.Errorf("want direnv allow and a warm-up, got %q", raw)
	}
}

func TestGoProvisionerRefusesAPathThatIsAFile(t *testing.T) {
	f := newGitFixture(t)
	job := f.job("feature/1-x", "")
	if err := os.MkdirAll(filepath.Dir(job.Worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job.Worktree, []byte("in the way\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.c.provisionWorktree(job, f.log); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want a refusal, got %v", err)
	}
}

// The dispatch: no script means the built-in provisioner, and it lands the directory wisp is
// going to look at.
func TestNoScriptMeansTheBuiltinProvisionerBuildsIt(t *testing.T) {
	f := newGitFixture(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WISP_PROGRAM", "")
	item := Item{Name: "repo/7-thing"}
	w := f.c.WorkflowFor(item, "")
	if !w.ProvisionsInGo() || len(w.Notes) != 0 {
		t.Fatalf("a bare workspace should provision in Go with no notes: inGo=%v notes=%v", w.ProvisionsInGo(), w.Notes)
	}
	entries := []Entry{{Repo: "repo", Branch: w.BranchFor(item, "repo")}}
	if err := f.c.EnsureWorktrees(w, item, entries, f.log); err != nil {
		t.Fatalf("EnsureWorktrees: %v", err)
	}
	wt := f.c.WorktreeFor(w, "repo", item)
	if !isDir(wt) {
		t.Errorf("no worktree at %s; logs %v", wt, f.logs)
	}
	if !f.logged("with git worktree") {
		t.Errorf("the provision window should say which provisioner ran: %v", f.logs)
	}
	// A second pass finds it and does nothing.
	before := len(f.logs)
	if err := f.c.EnsureWorktrees(w, item, entries, f.log); err != nil || len(f.logs) != before {
		t.Errorf("an existing worktree should be left alone: err=%v logs=%v", err, f.logs[before:])
	}
}

// A provision: some file wrote, that wisp would not run, is refused rather than swapped for the
// built-in provisioner, whichever reason it was withheld for.
func TestARefusedScriptIsNotSwappedForTheBuiltinProvisioner(t *testing.T) {
	f := newWorkflowFixture(t)
	if err := os.MkdirAll(filepath.Join(f.c.Workspace, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Repo: "repo", Branch: "feature/1-x"}}

	// Unaccepted, in the workspace config.
	f.script("bin/p.sh", "#!/bin/sh\nmkdir -p .worktrees/repo--1-x\n")
	f.spaceConfig("provision: bin/p.sh\n")
	w := f.c.WorkflowFor(Item{Name: "repo/1-x"}, "")
	if w.ProvisionsInGo() || w.Refused["provision"] == "" {
		t.Errorf("unaccepted .wisp.yaml provision: inGo=%v refused=%q", w.ProvisionsInGo(), w.Refused["provision"])
	}
	if err := f.c.EnsureWorktrees(w, Item{Name: "repo/1-x"}, entries, func(string) {}); err == nil || !strings.Contains(err.Error(), "did not run") {
		t.Errorf("want a refusal naming the withheld script, got %v", err)
	}
	if isDir(f.c.WorktreeRoot()) {
		t.Error("something provisioned around the refusal")
	}

	// Named by a bundle of yours, outside the workspace, but not there. Yours is not gated, so
	// the path stays and provisioning says the script is missing: still not the built-in.
	g := newWorkflowFixture(t)
	g.userBundle("mine", "name: mine\nhooks:\n  provision: bin/nowhere.sh\n")
	g.userConfig("workflow: mine\n")
	if err := os.MkdirAll(filepath.Join(g.c.Workspace, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	w = g.c.WorkflowFor(Item{Name: "repo/1-x"}, "")
	if w.ProvisionsInGo() || w.Hooks.Provision == "" {
		t.Errorf("a named script that is missing should stay named: inGo=%v provision=%q", w.ProvisionsInGo(), w.Hooks.Provision)
	}
	if err := g.c.EnsureWorktrees(w, Item{Name: "repo/1-x"}, entries, func(string) {}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("want the missing-script error, got %v", err)
	}

	// Named by the workspace's own file, inside it, but not there: refused and said.
	h := newWorkflowFixture(t)
	h.spaceConfig("provision: bin/nowhere.sh\n")
	h.trustSpaceConfig()
	w = h.c.WorkflowFor(Item{}, "")
	if w.ProvisionsInGo() || w.Refused["provision"] == "" || !hasNote(w, "not there") {
		t.Errorf("missing workspace script: inGo=%v refused=%q notes=%v", w.ProvisionsInGo(), w.Refused["provision"], w.Notes)
	}
}
