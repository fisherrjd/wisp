# wisp

One work item, one tmux session.

wisp turns a unit of work into a running workspace: it finds the item (locally or on GitLab), reprovisions any git worktrees it needs, writes a context file for the agent, and drops you into a tmux session named after it.

```
› ledger                                  airbook ●3 · eldo ?1      2/8
▌ ? ledger-service/318-double-entry-audit  │  Edit file src/reconcile.ts
  + ledger-service/327-backfill-entries    │
                                           │  Do you want to make this edit?
──────────────────────────────────────────────────────────────────────
● live  ? needs input  ○ folder  + gitlab     enter open  ctrl-n new
```

**Documentation:** [Commands](docs/commands.md) · [Configuration](docs/configuration.md) · [The picker](docs/picker.md) · [Sessions and worktrees](docs/sessions.md) · [Items](docs/items.md) · [Remote workspaces](docs/remote-workspaces.md) · [Workflows](docs/workflows.md) · [Hooks](docs/hooks.md) *(the reasoning behind them)*

## The model

Five nouns, and everything else follows from them.

| | |
|---|---|
| **System** | A machine. This one, or one you reach over ssh. It owns its workspaces and answers for them; nothing here keeps a copy of what it holds. |
| **Workspace** | The container. Every repo checkout, `docs/`, `.worktrees/` and the vault sit side by side inside it. Agents start here, so one cwd sees all of them. You can have several, and not everything you work on belongs in the same one. |
| **Item** | A folder in the vault, `<repo>/<iid>-<slug>` or `_adhoc/<name>`. The unit of work. Its stable identity is only `<repo>/<iid>`, because slugs drift between what you typed locally and what GitLab derives from the title. |
| **Worktree** | A cache, deliberately. Branches are the real state. Delete a worktree and reopening the item reprovisions it. |
| **Session** | A tmux session tagged `@wisp_item`. The `agent` window runs at the workspace root; one further window per worktree, for builds and dev servers, plus a transient `provision` window while any are still being built. |

Nothing wisp does destroys work. Killing a session leaves worktrees and branches; deleting a worktree leaves the branch; closing an item out leaves every file it holds.

## Three levels

Systems hold workspaces hold sessions, and wisp moves between all three.

| | |
|---|---|
| **session** | `next` / `prev`, or the picker's list. Never leaves the workspace, so what is running elsewhere cannot get in the way of flipping between the work in front of you. |
| **workspace** | `ctrl-w` for the tree, `hop` for the blind step. |
| **system** | `←` / `→` in that tree, a whole machine at a time. |

Hopping into a workspace lands on the session you were last in there, not on its picker, so a trip out and back is a round trip rather than a reset. The picker is the fallback for a workspace you have not opened anything in yet.

Each workspace has its own picker session, its own vault, its own GitLab group, and its own tmux sessions: two workspaces holding an item with the same slug get two sessions, not one. The picker's header carries the level above whatever you are looking at, a tally per machine, because an agent waiting on an answer on another one is the thing worth knowing from a list that shows neither.

## Usage

```
wisp                       go to the picker session, creating it if needed
wisp pick                  run the picker once, here, without a session
wisp open <item>           open an item directly
wisp next / wisp prev      cycle to the next or previous item session
wisp hop [next|prev|<ws>]  move to another workspace
wisp new <name|url>        make an item: a name, or a gitlab link
wisp done <item> [-m <line>]
                           close it out, writing the line into its notes;
                           --anyway closes it bare, --undo reopens it,
                           --list shows what has been closed out
wisp kill <item>           kill an item's session
wisp ls                    list live sessions, every workspace
wisp repos                 list workspace repos
wisp ws                    list workspaces
wisp ws new [-p] <name> [path]
                           make a directory a workspace and register it
wisp ws rm <name>          forget one; nothing on disk is touched
wisp host                  list the machines wisp can reach
wisp host add [name] <ssh target>
                           register a machine and everything it holds
wisp host rm <name>        forget a machine
wisp version               print the version

wisp -w <ws> <command>     run a command against a named workspace
```

`-w` has to come first, and `wisp ls` ignores it: that one always lists every workspace. `board`, `preview`, `provision` and `ws --json` also exist; they are mostly how one wisp asks another about a workspace it owns. Every flag and every error message is in [docs/commands.md](docs/commands.md).

## The picker

Type to filter, `enter` opens, `ctrl-n` creates (a name, or a pasted GitLab link), `ctrl-d` closes an item out, `ctrl-t` shows the closed-out ones again, `ctrl-w` opens the tree of machines and workspaces, `ctrl-x` kills the highlighted session, `ctrl-r` refreshes GitLab, `esc` quits and leaves everything running. Cursor keys are `↑` `↓` or `ctrl-k` `ctrl-j`; `ctrl-u` clears the line.

`ctrl-d` and `ctrl-x` are the two ways of being finished, and they are not the same one. `ctrl-x` stops what is running and leaves the item; `ctrl-d` ends the work and leaves the list, writing `done: true` into the item's `notes.md`. An item with a live session keeps its row either way: something is still on the machine, and hiding it would leave an agent with nothing pointing at it.

Closing out also asks what happened, when nothing has been written down yet. The flag used to be one keystroke and the write-up a trip to an editor, so the items that got closed out and the items that got written up were disjoint sets. Now `ctrl-d` on an item with an empty note opens a line, `enter` writes it under a dated heading and sets the flag together, and `ctrl-d` again closes it bare. An item you have already taken notes on closes with no ceremony.

A pasted GitLab link does not have to be assigned to you. Being the assignee is what fills the `+` section of the list, not what decides whether you can open something: reviewing a colleague's merge request is a link away.

`ctrl-w` swaps the list for the tree above it: machines as headers, their workspaces under them, same glyphs one layer up.

```
 workspaces                                  enter to go there
  airbook
▌   ○ near  (here)
  eldo  ?1
    ● work
    ✗ ghost
    ○ side
  gjallar
    ⚠ gjallar
```

Machines are rows too, because they are things you act on. `↑` `↓` (or `j` `k`) walk one row, `←` `→` (or `h` `l`) jump a whole machine, `esc` comes back.

| | on a workspace | on a machine |
|---|---|---|
| `enter` | go there | go to its default workspace |
| `n` | make a workspace **on this machine** | same |
| `a` | add a machine | add a machine |
| `x` | forget the workspace | forget the machine and everything it holds |

Plain letters, not chords: nothing in the tree types, and wisp lives inside tmux, so whichever chord you use as your prefix would never arrive. `n` reads the path on whichever machine the cursor is in, so there is no host prefix to remember and none to typo. The prompt says which.

Nothing there touches disk except making the vault and `.wisp.yaml` for a new workspace; forgetting only edits the config.

`hop` is the same move without the list, for a tmux binding where one key is the whole interface:

```
bind -N "wisp: next workspace" Up run-shell "wisp hop"
```

[docs/picker.md](docs/picker.md) has every key, every glyph and every refusal.

## Workspace layout

wisp reads and writes exactly these paths.

```
$WS/
├── .wisp.yaml                     this workspace's config
├── <repo>/                        any dir with a .git
├── docs/<repo>.md                 workspace doc
├── .worktrees/<repo>--<slug>/     the cache, safe to delete
├── .claude/scripts/provision-worktree.sh
└── working_items/                 the vault
    ├── <repo>/
    │   ├── <repo>.md              hub note, pulled into the agent's context
    │   └── <iid>-<slug>/
    │       ├── orchestration.md   manifest frontmatter, for multi-repo items
    │       ├── notes.md           shown in the preview pane; carries `done:`
    │       └── .wisp-context.md   generated on open, safe to delete
    └── _adhoc/<name>/             work with no ticket behind it
```

With no `notes.md`, the preview falls back to the first `.md` in the item folder.

## Configuration

`<workspace>/.wisp.yaml` wins over `~/.config/wisp/config.yaml`, and the environment wins over both.

```yaml
workflow: default                        # the bundle below supplies the rest
program: claude                          # runs in the agent window (default: claude)
install: false                           # install deps when provisioning
vault: working_items                     # where items live
worktrees: .worktrees                    # where the worktree cache goes
provision: .claude/scripts/provision-worktree.sh
gitlab:
  group: your-group/subgroup
  username: you
  # Recovers the repo directory from a work item URL. Exactly one capturing
  # group, which must be the repo. GitLab nests projects arbitrarily, so there
  # is no generic form for this.
  repo_pattern: '/subgroup/([^/]+)/-/'
  cache_ttl_min: 15
```

A **workflow** is a directory holding a `workflow.yaml` and its scripts, and it supplies `program`, the branch and worktree templates, the session layout and the hooks. A bare name is one of yours under `~/.config/wisp/workflows/`, `./name` is one the workspace ships, and every key a bundle does not set falls back to the built-in, so a workflow that changes one thing is four lines long. `wisp open <item> --workflow <name>` uses another one just once. [docs/workflows.md](docs/workflows.md) has the addressing rule and the five layers.

The user config, and only the user config, owns `workspaces:`, `hosts:` and `default:`. A workspace does not get to name its neighbours or its machines.

```yaml
# ~/.config/wisp/config.yaml
workspaces:
  work: ~/work
  side: ~/projects/side
default: work
```

`workspace: <path>` is the older single-workspace spelling and still works; it is folded into the set under its directory name.

Overrides: `WISP_WORKSPACE`, `WISP_PROGRAM`, `WISP_INSTALL` (any non-empty value, including `0`), and `XDG_CONFIG_HOME` for the config's location.

**Workspace resolution**, in order: `-w <name>`; `WISP_WORKSPACE`; the nearest ancestor holding a `.wisp.yaml` or a vault directory (so wisp works from inside a repo or a worktree); the default workspace; otherwise an error naming the fixes.

You never have to write either block by hand. `ctrl-w` then `n` or `a` in the picker does the same thing, and so does the shell:

```
wisp ws new side                 # adopt the current directory
wisp ws new side ~/projects/side # adopt one you already have
wisp ws new side -p ~/new/side   # create the directory too
wisp host add jade@eldo          # a machine, and everything on it
```

The picker's `n` always wants an explicit path; only the shell defaults it to the current directory.

It creates the vault and a commented `.wisp.yaml` to fill in, then registers the name. Running it on a directory that is already a workspace just registers it, which is how you make an existing vault reachable by `hop`. Both kinds of conflict are refused: a name already in use, and a path already registered under another name.

Without `-p` the directory has to exist. A mistyped path should fail there and then rather than become a workspace somewhere nobody meant to put one, where the mistake only surfaces later as a picker with nothing in it.

Editing the config by hand is still fine, and a path with no vault in it is reported as configured but missing rather than silently ignored: `wisp ws` says so, the workspace tree marks it `✗`, and `hop next` steps over it rather than stranding you there. Hopping to it by name still fails loudly, because you asked for that one specifically.

A workspace that is only ever reached by the upward search does not need to be in the config at all. It is named after its directory, which is enough to namespace its sessions; naming it in `workspaces:` is what makes it something you can `hop` to.

[docs/configuration.md](docs/configuration.md) has every key, its default, and what happens when it is missing.

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

Without an `orchestration.md`, a single-repo item is inferred from the folder's parent with branch `feature/<slug>`. `_adhoc` items get no repos at all, which is correct: no repo can be inferred, and the session is notes-only.

Provisioning shells out to your workspace's own script, with a fixed contract:

```
provision-worktree.sh <repo-path> <slug> <branch> --attach [--base <base>] [--no-install]
```

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

**The picker always runs locally. Only the agent session runs remotely.** Opening a remote item makes an ordinary wisp session here whose one window is an `ssh -t` into the machine that owns the work, so `next`, `prev`, the ring, the last-visited session and the needs-input check all keep working on it unchanged. Sessions running over there that you are not attached to come from asking the wisp on the far side, which needs to be installed and on its PATH **in a non-interactive ssh session** — the common failure is a wisp that only exists once `.bash_profile` has run.

Adding one is the same gesture as adding a local workspace, from the shell or from the picker's tree:

```
wisp host add jade@eldo                  # the machine, and everything on it
wisp host add box jade@eldo              # calling it something else
wisp ws new scratch bigbox:~/scratch     # one workspace it has not registered
wisp ws new scratch -p bigbox:~/scratch  # and make it there too
```

`a` and `n` in the picker's tree are the same two things. `-p` runs the same command on the far side rather than reaching into its filesystem. The ssh user belongs in the target, `jade@eldo`, so a machine whose account does not match your local one needs nothing in `~/.ssh/config`.

Beyond that wisp does no authentication. If `ssh eldo` works in your shell it works here, and if it does not, that is an ssh config problem with an ssh config fix. Background calls run under `BatchMode`, so key auth is not optional.

A machine that will not answer keeps its own row, marked `⚠`, rather than silently taking its workspaces out of the list. That is separate from the `✗` of a workspace that does not exist: both are unusable, but one is fixed by making a directory and the other by fixing ssh.

Forgetting a machine drops everything on it at once. `x` on one of its workspaces says so rather than pretending to remove a row that was never registered here.

Attaching stacks two tmux servers, so the prefix key means two things. The wrapper's status bar says which workspace and host you are in; what the prefix does is your tmux config's call.

[docs/remote-workspaces.md](docs/remote-workspaces.md) has the wire protocol, the ssh options, and every failure message.

## Requirements

| | |
|---|---|
| **tmux** | Required for everything. |
| **ssh** | Required for remote workspaces. Purely local use never invokes it. |
| **glab** | Optional. Without it the GitLab source is empty and pasting a link into `wisp new` fails; everything else is unaffected. |
| **git** | Not invoked by wisp itself. Your provisioning script needs it, and `wisp repos` looks for `.git` directories. |

Provisioning shells out to a `provision-worktree.sh` in the workspace, which owns branching, env file copying and dependency installation; wisp does not duplicate that.

## Install

```
nix build github:fisherrjd/wisp
```

Or as a flake input, with the overlay:

```nix
inputs.wisp.url = "github:fisherrjd/wisp";
# then, where you build pkgs:
nixpkgs.overlays = [ inputs.wisp.overlays.default ];
```

Without nix, which is the usual case on a server you only want the far end on:

```
go install github.com/fisherrjd/wisp@latest
```

A remote workspace needs wisp on both machines, at versions speaking the same wire; a mismatch says so by name and number rather than half-working.

For working on wisp itself, `default.nix` plus direnv gives the dev environment, and `nix develop` gives a second one from the flake.

## License

MIT.
