# Configuration

Two files, one environment, and a precedence order that is not the order you read them in.

---

## Precedence

Built-in defaults, then `~/.config/wisp/config.yaml`, then `<workspace>/.wisp.yaml`, then environment variables. Each step overlays the one before, key by key: a key absent from the later file keeps the earlier value.

**The workspace file wins over the user file.** The workspace is the unit that owns a vault, a set of repos and a GitLab group, so its config should travel with it rather than living in one global file that a second workspace would have to fight.

The user config lives at `$XDG_CONFIG_HOME/wisp/config.yaml` if that is set, otherwise `~/.config/wisp/config.yaml`. Deliberately XDG rather than the platform convention: on macOS the stdlib answer is `~/Library/Application Support`, but command line tools there conventionally use `~/.config`, and that is where anyone will look for this file.

**A parse error in either file is fatal and names the file.** Falling back to defaults on a broken user config made every workspace and machine vanish at once, and `wisp -w demo` then answered `no workspace named "demo"; known:` with an empty list, about a file with `demo` written plainly in it.

---

## What each file may contain

### Either file

| key | type | default | what it controls |
|---|---|---|---|
| `program` | string | `claude` | The command run in the session's `agent` window. wisp appends the context prompt as one shell-quoted argument, so flags belong here. |
| `install` | bool | `false` | Whether to install dependencies when provisioning a worktree. False passes `--no-install` to the script. |
| `vault` | string | `working_items` | The directory under the workspace where items live. Also doubles as the workspace-root marker in the upward search. |
| `worktrees` | string | `.worktrees` | Where the worktree cache goes. |
| `provision` | string | `.claude/scripts/provision-worktree.sh` | Workspace-relative path to the provisioning script. The one place wisp runs code you wrote. |
| `gitlab.group` | string | — | The group the queue is scoped to. |
| `gitlab.username` | string | — | The assignee the queue is filtered to. |
| `gitlab.repo_pattern` | string | — | Regex recovering a repo directory name from an item's web URL. Exactly one capturing group. |
| `gitlab.cache_ttl_min` | int | `15` | Minutes before the cached GitLab response is refreshed. |

```yaml
# <workspace>/.wisp.yaml
program: claude
install: false
vault: working_items
worktrees: .worktrees
provision: .claude/scripts/provision-worktree.sh
gitlab:
  group: your-group/subgroup
  username: you
  repo_pattern: '/subgroup/([^/]+)/-/'
  cache_ttl_min: 15
```

`cache_ttl_min: 0` is legal and means every load shells out to `glab`. That is a slow picker, not an error.

**`program:` is the whole of wisp's agent support, and there is deliberately no more of it.** The value is handed to tmux as a shell command line with the context file appended as one shell-quoted argument, which means flags, environment prefixes, pipelines and wrapper scripts are all already expressible, and it means wisp has no concept of a supported agent: no adapter to write, no list to be absent from.

```yaml
program: claude
program: codex
program: aider --model sonnet
program: OPENAI_BASE_URL=http://localhost:8080 my-agent
program: .claude/scripts/agent-wrapper.sh
```

The one contract is that the prompt arrives as an argument. An agent that only reads its prompt from stdin, or that expects to be typed into after it starts, needs a wrapper script that does the reading. That is the cost of having no adapters, and it is a file you write once.

`WISP_PROGRAM` overrides it for a single session without editing either file, which is how you try a different agent on one item.

### The user config only

| key | type | what it controls |
|---|---|---|
| `workspaces` | map of name to location | The set the ring hops between. |
| `default` | string | Which one is the fallback, and which one adopts sessions from before workspaces existed. If unset, the first name in sorted order. |
| `hosts` | list or map | The machines wisp can reach. |
| `workspace` | location | The older single-workspace spelling. Still works; folded into `workspaces:` under its directory name. |

```yaml
# ~/.config/wisp/config.yaml
workspaces:
  work: ~/work
  side: ~/projects/side
default: work
hosts:
  - jade@eldo
  - bigbox
```

These four are read from the user config **and nowhere else**. A workspace naming its neighbours or its machines would let one of them rename or hide another, so whatever a `.wisp.yaml` says about them is discarded rather than merged. That matters because `.wisp.yaml` is a file you check into a repo and hand to other people.

### Location shapes

Anywhere a workspace path is expected, four spellings work:

```yaml
workspaces:
  work:    ~/work                      # a path
  desktop: bigbox:~/work               # the scp shorthand, already in everyone's fingers
  laptop:  {host: mb, path: ~/w}       # the long form, for when there is more to say
  remote:  bigbox                      # a machine: its default workspace
```

A colon **before the first slash** means a host. That makes `bigbox:~/work` remote and `/var/lib/a:b` local, with no mode flag to set and no reading anyone has to be taught.

`~` is expanded by wisp, since a hand-edited YAML file will almost always contain one and the shell never gets a chance.

### Host shapes

```yaml
hosts:
  - jade@eldo                     # named after the machine
  - bigbox

hosts:                            # or a mapping, to call one something else
  box: jade@eldo
```

The list is the common case, where the name is the hostname and there is nothing to say. The name is derived by stripping the account, any port, and the domain: `jade@eldo.local` becomes `eldo`.

A hand-written list is converted to a mapping the first time `wisp host add` rewrites the file.

---

## Environment

| var | effect |
|---|---|
| `WISP_WORKSPACE` | The workspace root, as a path. Consulted only when `-w` was not given, and beats the upward search and the configured default. |
| `WISP_PROGRAM` | Overrides `program:`. Applied after both config files. |
| `WISP_INSTALL` | **Any non-empty value** sets `install: true`, including `WISP_INSTALL=0`. |
| `XDG_CONFIG_HOME` | Relocates the user config. |
| `TMUX` | Not wisp's, but read: it decides `switch-client` against `attach-session`, and gates `next`, `prev` and `hop`. |
| `TMPDIR` | Where the GitLab cache file goes. |

`WISP_PROGRAM` and `WISP_INSTALL` are **inert for a remote workspace named on this side**. Resolving `-w eldo` returns as soon as the host is known, before the environment is applied, because the far side runs its own `Load` and answers for its own config. Set them over there.

---

## Workspace resolution

In order:

1. **`-w <name>`**, when given. An explicit answer skips the search entirely.
2. **`WISP_WORKSPACE`**, when set.
3. **The nearest ancestor of the current directory** holding a `.wisp.yaml` or a vault directory. This is the git approach, and it is what lets wisp work from inside a repo or a worktree rather than only from the workspace root. It matters in practice: a session's own worktree windows are several levels below the root.
4. **The default workspace's path**, for running wisp from anywhere at all.
5. **The current directory**, only so the error message names somewhere you recognise.

One wrinkle in step 3: the vault name used by the search is the one known *before* `<workspace>/.wisp.yaml` is read, so it comes from the built-in default or your user config. A workspace that renames its vault only in its own `.wisp.yaml` is discoverable by its `.wisp.yaml` marker but not by its vault directory.

A workspace found this way and never configured is named after its directory, which is stable across runs and enough to namespace its sessions. Naming it under `workspaces:` is what makes it something you can `hop` to.

When resolution finds nothing, the error depends on how you asked. Naming a workspace that is configured but missing sends you to `wisp ws new -p`; searching upward and finding nothing sends you to `cd`, `wisp ws new`, or `WISP_WORKSPACE`. Telling someone wisp searched upward and found nothing, when they named the workspace themselves, sends them looking in the wrong place entirely.

---

## Writing it without an editor

Nothing above has to be typed by hand. `ctrl-w` then `n` or `a` in the picker does the same work, and so does the shell:

```
wisp ws new side                 # adopt the current directory
wisp ws new side ~/projects/side # adopt one you already have
wisp ws new side -p ~/new/side   # create the directory too
wisp host add jade@eldo          # a machine, and everything on it
```

Both kinds of conflict are refused: a name already in use, and a path already registered under another name. Edits go through a YAML node, so comments survive.

Hand-editing stays fine. A path with no vault in it is reported as configured but missing rather than silently ignored: `wisp ws` says so, the workspace tree marks it `✗`, and `hop next` steps over it. Hopping to it by name still fails loudly, because you asked for that one specifically.

---

## The GitLab source

Off until `group`, `username` and `repo_pattern` are all set. It says which are missing:

```
gitlab source off: set gitlab.group, gitlab.username in /Users/you/work/.wisp.yaml
```

Said out loud rather than returning an empty list, because an unconfigured source and a source with no assigned items look identical in the picker. The silent version cost a real debugging session: after the group and username stopped being hardcoded, a workspace with no `.wisp.yaml` simply showed fewer rows and gave no reason.

### `repo_pattern`

It is matched against the whole work-item URL, not against a project path, so it can anchor on the segments around the repo name. It needs exactly one capturing group, and that group must be the repo directory as it is spelled in your workspace.

GitLab nests projects arbitrarily, so there is no generic form for this and no sensible default. For `https://gitlab.example.com/your-group/subgroup/wisp/-/issues/42`:

```yaml
repo_pattern: '/subgroup/([^/]+)/-/'
```

Anchoring on the literal `/-/` is what stops the group segment being captured.

### The cache

The raw query response is cached at `$TMPDIR/wisp-gitlab-<hash of workspace path>.json`, refreshed when older than `cache_ttl_min`. It is namespaced by workspace because the bash version used one fixed filename, and pointing `WISP_WORKSPACE` at a second tree served it the first tree's items.

A failed refresh falls back to a stale cache rather than emptying the picker. `ctrl-r` drops the cache and re-queries.

The query asks for open work items **assigned to you**, across the group and its descendants, capped at the first 100. That cap is silent: if you are the assignee on more than a hundred open items, the rest do not appear and nothing says so.

Being the assignee is what fills that list. It is not what decides whether you can open something — pasting a link works regardless.
