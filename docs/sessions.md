# Sessions, windows and worktrees

What actually happens between pressing `enter` and having somewhere to work.

---

## Opening an item

If a session already exists for the item, wisp attaches to it and does nothing else. No manifest read, no context rewrite, no worktree check. **The layout below happens once, on first open.**

The lookup is by identity, not by name. A session started by an older wisp is named differently but is still this item's session, and creating a second one beside it would split the work.

Otherwise, in order:

1. Create the vault folder if it is missing, with a `notes.md` stub. An item picked straight off GitLab has never had one here, and without it the context file had nothing to write into, so the agent started with no prompt at all, not even the name of the item it was on.
2. Read the manifest from `orchestration.md`, or infer it.
3. Write `.wisp-context.md`.
4. Create the session, one window named `agent`, at the **workspace root**, running your `program:` with the context as its argument.
5. Tag the session with the item and the workspace, and set a 10,000-line scrollback.
6. Add one window per worktree that already exists on disk.
7. If any worktree is still missing, add a `provision` window running `wisp provision <item>`.
8. Focus `agent`.
9. Record the session as this workspace's last visited, so a hop out and back returns to it.
10. Attach.

The tmux calls, in the order they are made:

```
tmux new-session -d -s <session> -n agent -c <workspace> '<program> <prompt>'
tmux set-option -t <session> history-limit 10000
tmux set-option -t <session> @wisp_item <item>
tmux set-option -t <session> @wisp_ws <workspace>
tmux list-windows -t <session> -F '#{window_name}'
tmux new-window -t <session> -d -n <repo> -c <worktree>          # per existing worktree
tmux new-window -t <session> -n provision -c <workspace> '<wisp> provision <item>'
tmux select-window -t <session>:agent
```

`select-window` targets the window **by name**: `base-index` may be 1, so `session:0` is not reliably the first window. Every `-t` that names a session uses a `=` prefix for exact matching.

Attaching uses `switch-client` from inside tmux and `attach-session -u` from outside. The `-u` matters more than it looks: without it tmux only writes UTF-8 when the first set variable of `LC_ALL`, `LC_CTYPE` or `LANG` contains `UTF-8`, and on a bare ssh login none of them is set. The symptom is misleading, because the pane buffer holds correct UTF-8 and `capture-pane` looks fine while every multibyte glyph reaches the terminal mangled.

wisp then replaces itself with tmux rather than spawning it. There is nothing left to do, and staying alive would leave a useless parent process wrapping the session for as long as it lives.

---

## Session names

```
wisp_<workspace>_<item>
```

Both halves are sanitised to `[a-zA-Z0-9_-]`, because tmux forbids `.` and `:` in session names. The workspace half additionally has underscores folded to dashes, or a workspace whose own name held one would make the boundary between the two halves ambiguous. So workspace `my_ws` and item `wisp/42-thing` give `wisp_my-ws_wisp-42-thing`.

The workspace in the name is not decoration. Without it, two workspaces holding an item with the same slug collapsed onto one session.

The name is lossy twice over, so it is not the identity. The real answer is in two tmux user options, `@wisp_item` and `@wisp_ws`, read back through a single `tmux ls` format string. That is what lets a session created by an older wisp, under the old naming, still be found and reattached rather than quietly duplicated alongside a second one.

A session carrying no `@wisp_ws` predates workspaces and is adopted by the default one, so upgrading wisp with work already running does not strand it.

Home sessions are `wisp-<workspace>`, joined with a dash rather than the underscore of the prefix, so they never appear in the list of items.

---

## Windows

| window | cwd | runs |
|---|---|---|
| `agent` | the workspace root | `program:`, with the context file as its argument |
| one per repo | that repo's worktree | a plain shell |
| `provision` | the workspace root | `wisp provision <item>`, then closes itself |

The agent sits at the workspace root because one cwd there sees the vault, `docs/`, every repo and every worktree. That is the whole point of the container model.

The repo windows are **shells, not agents**: they are for builds and dev servers. They are created detached so they do not steal focus. Their names are the repo truncated to twelve characters.

An `_adhoc` item gets exactly one window. No repo can be inferred and the session is notes-only.

Two hooks sit around a session. `open` runs once the windows are built and the session is tagged, before the attach, and cannot stop it; `kill` runs after `kill-session` has succeeded, and cannot undo it. Both are the workflow's ([Hooks](hooks.md#open-and-kill-around-a-session)).

The window set is derived from the manifest, not configured. `program:` is the only part of the layout you can change.

It is also the only part that needs to be. wisp never wraps the agent: `program:` goes to tmux as a shell command line and the process owns the pane from there, which is why `tmux attach` reaches it with wisp entirely out of the loop, and why wisp has no concept of a supported agent to add yours to. See [configuration.md](configuration.md) for what can go in the value.

---

## The context file

`<vault>/<item>/.wisp-context.md`, regenerated on every open, safe to delete.

```markdown
# Session context: payments-api/1042-retry-backoff

Generated by wisp. cwd is the workspace root, so every path below is relative to it.

## This item

- `working_items/payments-api/1042-retry-backoff/orchestration.md`
- `working_items/payments-api/1042-retry-backoff/notes.md`

## Repos in this item

### payments-api

- branch `feature/1042-retry-backoff` from `main`
- worktree `.worktrees/payments-api--1042-retry-backoff`
- hub note `working_items/payments-api/payments-api.md`
- workspace doc `docs/payments-api.md`
```

An item touching two repos pulls two repo contexts, not one per repo in the workspace. Every line is conditional, and what it omits is what the agent will not know about.

The worktree is listed whether or not it exists yet, marked `(still provisioning, wait for it before building)` when it does not. Provisioning runs in the background so the session can open immediately, and the agent needs to know where the checkout is going to be, not only where it already is. The file is rewritten once provisioning finishes, so an agent re-reading it sees real paths.

The file is **inlined into the startup prompt** rather than pointed at, as long as it is under 8 KB. Telling the agent to read it costs a full tool-call round trip before it can start, several seconds of latency to fetch a file that is typically under 300 bytes. Past that bound the prompt names the path instead.

---

## Worktrees

```
<workspace>/.worktrees/<repo>--<slug>
```

A worktree is **a cache, deliberately**. Branches are the real state. Deleting a worktree is safe and reopening the item recreates it. That is also why the manifest records intent — repo, branch, base — and not paths: a path is a cache, and a deleted worktree should be a cache miss rather than lost state.

### The manifest

YAML frontmatter in the item's `orchestration.md`:

```yaml
---
repos:
  - repo: payments-api
    branch: feature/1042-retry-backoff
    base: main
  - repo: webhooks-worker
    branch: feature/1042-retry-backoff
---
```

A missing `branch:` defaults to `feature/<slug>`. Entries with no `repo:` are dropped.

With no file, no readable frontmatter, or no usable entries, a single-repo item is inferred: the repo is the folder's parent and the branch follows the slug. `_adhoc` items get nothing, which is correct.

An unclosed fence is reported rather than ignored — `frontmatter opened with --- but never closed` — because it is almost always a half-written file.

Keys wisp does not know are left alone. Real `orchestration.md` files carry an `item:` number and a plan table; neither is read.

### Provisioning

wisp never runs `git worktree add` itself. It shells out to a script in your workspace:

```
<workspace>/.claude/scripts/provision-worktree.sh <repo-path> <slug> <branch> --attach [--base <base>] [--no-install] [--worktree <path>]
```

That contract is the whole interface. The script already handles branching from the remote default, copying `default.nix` and `.envrc`, `direnv allow` and the dependency install, and duplicating any of that inside wisp would be a second source of truth.

`--attach` is always passed. The contract is "give me a worktree for this branch", and an item reopened after cleanup has a live branch but no checkout; without it, every reconstitution fails on "branch already exists".

`--no-install` is passed unless `install:` is on. Nix and direnv still give a working toolchain, and `node_modules` for a large monorepo is far too much to pay for opening an item in order to read it.

`--worktree` is passed only when the workflow's `worktree:` template resolves to something other than the directory the script would derive on its own. That condition is deliberate, and it is what keeps the default contract byte for byte what it has always been: a workspace that never set `worktree:` sends the same argv it sent before workflows existed, and no existing script has to learn a new flag to keep working.

It exists because the two halves had drifted. `worktree:` decided where wisp *looked* for the checkout, while the script went on deriving `<repo>--<slug>` for itself, so any workflow that changed the template opened a session that could never find its own worktree: the per-repo window never appeared and the context file said "still provisioning" forever. Passing the destination is the smaller half of the fix. The other half is that wisp now checks the directory is there after the script exits zero, and names the path, the template and the flag if it is not, because a script that quietly ignores an argument it does not understand is exactly how the first version failed silently.

Failures are graded. A repo named in the manifest that is not a directory here is logged and skipped. A script that exits non-zero is logged and the other repos are still attempted. A **missing** script aborts the run, because nothing after it can work.

### Why it happens in a side window

Provisioning does not block opening. The script pre-warms a nix environment through direnv, and a cold evaluation takes long enough that waiting for it before showing the session felt like the tool had hung, with a nix build scrolling past for something the agent does not need. The agent window needs none of it: it runs at the workspace root and reads the context file.

So the session opens straight away and any missing worktrees are built in their own window, which adds its own windows when done and closes itself. In a window rather than a goroutine so the work is visible and interruptible rather than hidden behind a frozen picker.

If it fails, the window prints the error and waits for a keypress. tmux closes a window the moment its command returns, and a failure that vanishes is one nobody can read.

---

## Live, and waiting on you

wisp asks tmux for its sessions once, with a format string that returns the user options alongside the names, then captures each pane in parallel to decide which are waiting.

```
tmux ls -F '#{session_name}\t#{@wisp_item}\t#{@wisp_ws}'
tmux capture-pane -p -J -t <session>
```

A session is `needs input` when its pane contains the literal string `No, and tell Claude what to do differently`.

That is best-effort by construction, and worth knowing: it is a string from Claude Code's permission dialog, borrowed from claude-squad. If that copy is reworded the `?` state silently stops appearing. The preview pane is the reliable signal.

The scan is bundled with the item list because both are needed at the same moment and the capture is one call per session; gathering them separately would pay that cost twice. There is no timer. It runs when the picker loads, after a kill or a close-out, on `ctrl-r`, and once per turn of the home loop.

---

## The home loop

`wisp` bare is home: the picker in its own session, which is the thing you come back to.

```sh
while true; do WISP_WORKSPACE='<path>' '<wisp>' pick; done
```

One session per workspace, one window, status bar off.

This exists because the picker used to be a popup over whatever session you happened to be in, which made it transient and translucent rather than somewhere you navigate to. As a real session it is a fixed destination: switch to it, pick an item, switch to that item, switch back. The tmux client stack does the work and nothing overlays anything.

Looping is what makes it a home rather than a one-shot. Picking an item replaces the process with `tmux switch-client`, and quitting exits it; either way the loop draws the picker again, so returning always lands on the list, freshly loaded.

Explicitly `pick`, never bare `wisp`: bare wisp means home, so a bare invocation here would have the home session spawning home sessions forever. And explicitly pinned to its workspace, because the loop must keep showing the same one even if the directory it started in stops resolving there.

For a remote workspace the loop is pinned by name instead, `wisp -w <name> pick`, and runs in a directory that exists on this machine. The picker runs here even then: drawing it on the far side would make every keystroke a network round trip for the sake of redrawing a list, and only the agent session needs to be over there.

`esc` **detaches** rather than exiting. Home runs the picker in a loop, so a plain exit is invisible: the loop redraws it and `esc` looks broken. Detaching means leaving tmux and getting the terminal back. Nothing is lost — the server keeps running, every item session stays exactly where it was, agents included, and the next `wisp` re-attaches to a picker that is already warm.

---

## Killing

`wisp kill` and `ctrl-x` do the same thing. For a remote item it kills the session on the machine doing the work first, then the local wrapper.

The wrapper usually dies on its own: killing the far side ends the ssh, which closes the window, which takes the session with it. Losing that race is not a failure, so the local kill only reports an error if the session is still standing afterwards.

Nothing wisp does destroys work. A kill leaves worktrees, branches, the vault folder, `notes.md`, `orchestration.md` and the context file. Deleting a worktree leaves the branch.
