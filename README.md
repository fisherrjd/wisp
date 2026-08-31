# wisp

One work item, one tmux session.

wisp turns a unit of work into a running workspace: it finds the item (locally or on GitLab), reprovisions any git worktrees it needs, writes a context file for the agent, and drops you into a tmux session named after it.

```
› ledger                                     work ●3 · side ?1      2/8
▌ ? ledger-service/318-double-entry-audit  │  Edit file src/reconcile.ts
  + ledger-service/327-backfill-entries    │
                                           │  Do you want to make this edit?
──────────────────────────────────────────────────────────────────────
● live  ? needs input  ○ folder  + gitlab     enter open  ctrl-n new
```

## The model

Four nouns, and everything else follows from them.

| | |
|---|---|
| **Workspace** | The container. Every repo checkout, `docs/`, `.worktrees/` and the vault sit side by side inside it. Agents start here, so one cwd sees all of them. You can have several, and not everything you work on belongs in the same one. |
| **Item** | A folder in the vault, `<repo>/<iid>-<slug>` or `_adhoc/<name>`. The unit of work. Its stable identity is only `<repo>/<iid>`, because slugs drift between what you typed locally and what GitLab derives from the title. |
| **Worktree** | A cache, deliberately. Branches are the real state. Delete a worktree and reopening the item reprovisions it. |
| **Session** | A tmux session tagged `@wisp_item`. Window 1 is the agent at the workspace root; one further window per worktree, for builds and dev servers. |

Nothing wisp does destroys work. Killing a session leaves worktrees and branches; deleting a worktree leaves the branch.

## Two rings

Sessions form a ring you walk with `next` and `prev`. Workspaces form a ring above it, walked with `hop`. `next` never leaves the workspace you are in, so what else is running elsewhere cannot get in the way of flipping between the work in front of you.

Hopping into a workspace lands on the session you were last in there, not on its picker, so a trip out and back is a round trip rather than a reset. The picker is the fallback for a workspace you have not opened anything in yet.

Each workspace has its own picker session, its own vault, its own GitLab group, and its own tmux sessions: two workspaces holding an item with the same slug get two sessions, not one. The picker's header carries the only cross-workspace view, a tally per workspace, because an agent waiting on an answer somewhere else is the one thing worth knowing from inside a list that does not show it.

## Usage

```
wisp                       go to the picker session, creating it if needed
wisp pick                  run the picker once, here, without a session
wisp open <item>           open an item directly
wisp next / wisp prev      cycle to the next or previous item session
wisp hop [next|prev|<ws>]  move to another workspace
wisp ls                    list live sessions, every workspace
wisp ws                    list workspaces
wisp ws new [-p] <name> [path]
                           make a directory a workspace and register it
wisp ws rm <name>          forget one; nothing on disk is touched
wisp kill <item>           kill an item's session
wisp repos                 list workspace repos

wisp -w <ws> <command>     run any of the above against a named workspace
```

In the picker: type to filter, `enter` opens, `ctrl-n` creates (a name, or a pasted GitLab link), `ctrl-w` opens the workspaces, `ctrl-x` kills the highlighted session, `ctrl-r` refreshes GitLab, `esc` quits and leaves everything running.

`ctrl-w` swaps the list for the workspaces: same glyphs one layer up, `enter` goes there, `ctrl-n` makes a new one (`<name> <path>`, with `-p` to create the directory), `ctrl-x` forgets one, `esc` comes back. Nothing there touches disk except making the vault for a new workspace; forgetting one only edits the config.

`hop` is the same move without the list, for a tmux binding where one key is the whole interface:

```
bind -N "wisp: next workspace" Up run-shell "wisp hop"
```

## Workspace layout

wisp reads and writes exactly these paths.

```
$WS/
├── <repo>/                        any dir with a .git
├── docs/<repo>.md                 workspace doc
├── .worktrees/<repo>--<slug>/     the cache, safe to delete
├── .claude/scripts/provision-worktree.sh
└── working_items/                 the vault
    ├── <repo>/<iid>-<slug>/
    │   ├── orchestration.md       manifest frontmatter, for multi-repo items
    │   ├── notes.md               shown in the preview pane
    │   └── .wisp-context.md       generated on open, safe to delete
    └── _adhoc/<name>/             work with no ticket behind it
```

## Configuration

`<workspace>/.wisp.yaml` first, then `~/.config/wisp/config.yaml`, then the environment.

```yaml
program: claude --permission-mode auto   # runs in window 1
install: false                           # install deps when provisioning
gitlab:
  group: your-group/subgroup
  username: you
  # Recovers the repo directory from a work item URL. Exactly one capturing
  # group, which must be the repo. GitLab nests projects arbitrarily, so there
  # is no generic form for this.
  repo_pattern: '/subgroup/([^/]+)/-/'
  cache_ttl_min: 15
```

The user config, and only the user config, owns the set of workspaces. A workspace does not get to name its neighbours.

```yaml
# ~/.config/wisp/config.yaml
workspaces:
  work: ~/work
  side: ~/projects/side
default: work
```

`workspace: <path>` is the older single-workspace spelling and still works; it is folded into the set under its directory name.

Overrides: `WISP_WORKSPACE`, `WISP_PROGRAM`, `WISP_INSTALL`.

**Workspace resolution**, in order: `-w <name>`; `WISP_WORKSPACE`; the nearest ancestor holding a `.wisp.yaml` or a vault directory (so wisp works from inside a repo or a worktree); the default workspace; otherwise an error naming the fixes.

You never have to write that block by hand. `ctrl-w` then `ctrl-n` in the picker does the same thing, and `wisp ws new` does it from a shell:

```
wisp ws new side                 # adopt the current directory
wisp ws new side ~/projects/side # adopt one you already have
wisp ws new side -p ~/new/side   # create the directory too
```

It creates the vault and a commented `.wisp.yaml` to fill in, then registers the name. Running it on a directory that is already a workspace just registers it, which is how you make an existing vault reachable by `hop`. Both kinds of conflict are refused: a name already in use, and a path already registered under another name.

Without `-p` the directory has to exist. A mistyped path should fail there and then rather than become a workspace somewhere nobody meant to put one, where the mistake only surfaces later as a picker with nothing in it.

Editing the config by hand is still fine, and a path with no vault in it is reported as configured but missing rather than silently ignored: `wisp ws` says so, the picker's header marks it `✗`, and `hop next` steps over it rather than stranding you there. Hopping to it by name still fails loudly, because you asked for that one specifically.

A workspace that is only ever reached by the upward search does not need to be in the config at all. It is named after its directory, which is enough to namespace its sessions; naming it in `workspaces:` is what makes it something you can `hop` to.

## The manifest

An item can span several repos. YAML frontmatter in `orchestration.md` says so:

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

It records intent, and deliberately not worktree paths: a path would be a cache pretending to be state.

## Remote workspaces

You register a **machine**, not a path. The machine already knows what it holds, so listing its workspaces here as well would be a second copy of a list only one side owns, and the two would drift the moment you made one over there.

```yaml
hosts:
  - jade@eldo                     # named after the machine
  - bigbox

hosts:                            # or a mapping, to call one something else
  box: jade@eldo
```

Everything on it joins the ring, and opening an item there puts you in the agent running on that machine. The machine's default workspace takes its bare name, since a machine usually holds one and calling it `eldo/work` where `eldo` would do is ceremony; anything else it holds is `eldo/side`.

```
› ▏                              near ●2 · eldo ?1 · eldo/side · box ⚠      4/9
```

**The picker always runs locally. Only the agent session runs remotely.** Opening a remote item makes an ordinary wisp session here whose one window is an `ssh -t` into the machine that owns the work, so `next`, `prev`, the ring, the last-visited session and the needs-input check all keep working on it unchanged. Sessions running over there that you are not attached to come from asking the wisp on the far side, which needs to be installed and on its PATH.

Adding one is the same gesture as adding a local workspace, from the shell or from `ctrl-w` `ctrl-n` in the picker:

```
wisp ws new eldo jade@eldo:              # the machine, and everything on it
wisp ws new scratch bigbox:~/scratch     # one workspace it has not registered
wisp ws new scratch -p bigbox:~/scratch  # and make it there too
```

The trailing colon separates the two: a host with nothing after it means the machine. `-p` runs the same command on the far side rather than reaching into its filesystem. The ssh user belongs in the target, `jade@eldo`, so a machine whose account does not match your local one needs nothing in `~/.ssh/config`.

Beyond that wisp does no authentication. If `ssh eldo` works in your shell it works here, and if it does not, that is an ssh config problem with an ssh config fix.

A machine that will not answer keeps its own row, marked `⚠`, rather than silently taking its workspaces out of the list. That is separate from the `✗` of a workspace that does not exist: both are unusable, but one is fixed by making a directory and the other by fixing ssh.

Forgetting a machine drops everything on it at once. `ctrl-x` on one of its workspaces says so rather than pretending to remove a row that was never registered here.

Attaching stacks two tmux servers, so the prefix key means two things. The wrapper's status bar says which workspace and host you are in; what the prefix does is your tmux config's call.

[docs/remote-workspaces.md](docs/remote-workspaces.md) has the design and what is still missing.

Without an `orchestration.md`, a single-repo item is inferred from the folder's parent with branch `feature/<slug>`. `_adhoc` items get no repos at all, which is correct: no repo can be inferred, and the session is notes-only.

## Requirements

`tmux` and `git`. `glab` is optional; without it the GitLab source is simply empty. Provisioning shells out to a `provision-worktree.sh` in the workspace, which owns branching, env file copying and dependency installation; wisp does not duplicate that.

## Install

```
nix build github:fisherrjd/wisp
```

Or as a flake input, with the overlay:

```nix
inputs.wisp.url = "github:fisherrjd/wisp";
# then: (final: prev: { }) // inputs.wisp.overlays.default
```

## License

MIT.
