# wisp

One work item, one tmux session.

wisp turns a unit of work into a running workspace: it finds the item (locally or on GitLab), reprovisions any git worktrees it needs, writes a context file for the agent, and drops you into a tmux session named after it.

```
› ledger                                                          2/8
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
| **Workspace** | The container. Every repo checkout, `docs/`, `.worktrees/` and the vault sit side by side inside it. Agents start here, so one cwd sees all of them. |
| **Item** | A folder in the vault, `<repo>/<iid>-<slug>` or `_adhoc/<name>`. The unit of work. Its stable identity is only `<repo>/<iid>`, because slugs drift between what you typed locally and what GitLab derives from the title. |
| **Worktree** | A cache, deliberately. Branches are the real state. Delete a worktree and reopening the item reprovisions it. |
| **Session** | A tmux session tagged `@wisp_item`. Window 1 is the agent at the workspace root; one further window per worktree, for builds and dev servers. |

Nothing wisp does destroys work. Killing a session leaves worktrees and branches; deleting a worktree leaves the branch.

## Usage

```
wisp                    go to the picker session, creating it if needed
wisp pick               run the picker once, here, without a session
wisp open <item>        open an item directly
wisp next / wisp prev   cycle to the next or previous item session
wisp ls                 list live sessions
wisp kill <item>        kill an item's session
wisp repos              list workspace repos
```

In the picker: type to filter, `enter` opens, `ctrl-n` creates (a name, or a pasted GitLab link), `ctrl-x` kills the highlighted session, `ctrl-r` refreshes GitLab, `esc` quits and leaves everything running.

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

The user config may also set `workspace:`, which is where wisp goes when run from outside any workspace.

Overrides: `WISP_WORKSPACE`, `WISP_PROGRAM`, `WISP_INSTALL`.

**Workspace resolution**, in order: `WISP_WORKSPACE`; the nearest ancestor holding a `.wisp.yaml` or a vault directory (so wisp works from inside a repo or a worktree); the `workspace:` key; otherwise an error naming all three fixes.

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
