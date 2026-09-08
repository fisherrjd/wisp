# Configuration

Two files, one environment, and a precedence order that is not the order you read them in.

---

## Precedence

Built-in defaults, then `~/.config/wisp/config.yaml`, then `<workspace>/.wisp.yaml`, then environment variables. Each step overlays the one before, key by key: a key absent from the later file keeps the earlier value.

**The workspace file wins over the user file.** The workspace is the unit that owns a vault, a set of repos and a GitLab group, so its config should travel with it rather than living in one global file that a second workspace would have to fight.

The user config lives at `$XDG_CONFIG_HOME/wisp/config.yaml` if that is set, otherwise `~/.config/wisp/config.yaml`. Deliberately XDG rather than the platform convention: on macOS the stdlib answer is `~/Library/Application Support`, but command line tools there conventionally use `~/.config`, and that is where anyone will look for this file.

**A parse error in either file is fatal and names the file.** Falling back to defaults on a broken user config made every workspace and machine vanish at once, and `wisp -w demo` then answered `no workspace named "demo"; known:` with an empty list, about a file with `demo` written plainly in it.

**Workflow keys are the exception**, and they are most of what these files hold in practice. `workflow:`, `program:`, `branch:`, `worktree:`, the hooks, `layout:` and `status:` are read out of these two files separately, and resolved across five layers rather than two: a bundle sits under both files, and an item's `orchestration.md` sits over them. The order between the two files is the same. [Workflows](workflows.md) has the rest, and `wisp workflow` prints the answer with the file that supplied each key beside it.

---

## What each file may contain

### Either file

| key | type | default | what it controls |
|---|---|---|---|
| `install` | bool | `false` | Whether to install dependencies when provisioning a worktree. False passes `--no-install` to the script. |
| `vault` | string | `working_items` | The directory under the workspace where items live. Also doubles as the workspace-root marker in the upward search. |
| `worktrees` | string | `.worktrees` | Where the worktree cache goes. |
| `cache_ttl_min` | int | unset | Minutes before whichever source this workspace has is asked again. Source-neutral. Unset, not zero, is what defers to `gitlab.cache_ttl_min`. |
| `gitlab.group` | string | — | The group the queue is scoped to. |
| `gitlab.username` | string | — | The assignee the queue is filtered to. |
| `gitlab.repo_pattern` | string | — | Regex recovering a repo directory name from an item's web URL. Exactly one capturing group. |
| `gitlab.cache_ttl_min` | int | `15` | The older spelling of `cache_ttl_min`, and still where the default lives. |

```yaml
# <workspace>/.wisp.yaml
install: false
vault: working_items
worktrees: .worktrees
cache_ttl_min: 15
gitlab:
  group: your-group/subgroup
  username: you
  repo_pattern: '/subgroup/([^/]+)/-/'
```

**One TTL, two spellings.** `cache_ttl_min` is top-level because it times whatever source a workspace has, which is what it was always doing: a `source` hook's cache is on the same clock as the GitLab query's, so that `ctrl-r` means the same thing whichever one is behind it. `gitlab.cache_ttl_min` still works and is read as the older spelling of the same key, so no config had to change.

The top-level key wins whenever the file mentions it at all, including when it says `0`. Absent and zero are held apart deliberately, because zero is a useful value rather than a missing one: `cache_ttl_min: 0` means every load shells out to `glab`, or to your `source` hook. That is a slow picker, not an error, and the newer spelling had to be able to say something the older one always could.

### Workflow keys, in either file

`program:` and `provision:` used to be config keys and are now **workflow keys**, along with everything else about how a session is laid out and where work comes from. They can still be written in either config file and still mean what they always meant; what changed is that they are resolved per key across the workflow's five layers rather than through the config merge, and that they have no default in the config at all. That distinction is what lets `""` mean "said nothing" rather than "said `claude`".

| key | default | what it controls |
|---|---|---|
| `workflow` | the built-in | The bundle this file binds to. A bare name is one of yours under `~/.config/wisp/workflows/`, a leading `./` is one this workspace ships in `.wisp/workflows/`. |
| `program` | `claude` | The command run in the session's `agent` window. wisp appends the context prompt as one shell-quoted argument, so flags belong here. |
| `branch` | `feature/{slug}` | The branch an item's repo gets when its manifest does not name one. |
| `worktree` | `{repo}--{slug}` | The directory name inside `worktrees:` for one repo's checkout. |
| `provision` | `.claude/scripts/provision-worktree.sh` | Path to the provisioning script. The one place wisp has always run code you wrote. |
| `source`, `context`, `close` | none | The other three hooks. Also spellable nested under `hooks:`, which wins if both are written, and says so. |
| `layout` | one `agent`, one per worktree, one `provision` | The session's tmux windows. |
| `status.needs_input` | Claude Code's permission dialog line | The pane text that means an agent is waiting on a human. Workspace-level: an item that sets it is refused. |

Writing a hook both ways in one file is no longer resolved silently:

```
  note: context is set both as `context:` and under `hooks:`; the one under hooks wins
```

Every other conflict in resolution already produced a note, and "the nested one wins" is not a rule anyone would guess from a file that sets both.

```yaml
# <workspace>/.wisp.yaml
workflow: solo          # the bundle supplies the rest
context: .wisp/house-briefing.sh    # overriding one key of it
```

A relative hook path written **here** resolves against the workspace root, which is what a path in `.wisp.yaml` has always meant. The same key inside a bundle resolves against the bundle directory instead, so the bundle stays copyable. [Workflows](workflows.md) has the full table, the five layers and the addressing rule; `wisp workflow` prints what won and which file it came from.

### The user config only

| key | type | what it controls |
|---|---|---|
| `workspaces` | map of name to location | The set the ring hops between. |
| `default` | string | Which one is the fallback, and which one adopts sessions from before workspaces existed. If unset, the first name in sorted order. |
| `hosts` | list or map | The machines wisp can reach. |
| `workspace` | location | The older single-workspace spelling. Still works; folded into `workspaces:` under its directory name. |
| `accepted` | map of `<workspace> <thing>` to hash | Which workspace-supplied things have been read and allowed to run: a bundle address, `.wisp.yaml`, an item name, or `script <path>` for one hook script. Written by `wisp workflow accept`. |

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

These five are read from the user config **and nowhere else**. A workspace naming its neighbours or its machines would let one of them rename or hide another, so whatever a `.wisp.yaml` says about them is discarded rather than merged. That matters because `.wisp.yaml` is a file you check into a repo and hand to other people.

`accepted:` is on that list for a sharper reason than the rest: it is the record that decides whether a workspace's own checked-in workflow may execute at all, and a workspace that could write it would be accepting itself.

```yaml
# ~/.config/wisp/config.yaml
accepted:
  /Users/you/work ./ship: 854659096926d77dbfe636122cf24cd7cc39e87426d7a46009cae5bbb6169f8f
  /Users/you/work script .claude/scripts/provision-worktree.sh: 8d2b15d4e6fa26ea392e51e17c50e9b91951f638042374bd67821ac328c8339a
```

The key is the workspace path and the thing together, because the same relative address in two workspaces is two different directories, and the same relative script path is two different scripts. The value is a SHA-256 of what was accepted, re-checked on every load, so editing it puts it back to unaccepted.

A `script <path>` entry is one hook script, hashed by its own contents and recorded whoever named it: the built-in, your user config, the workspace config, a bundle or an item. Location is what decides, not provenance, so only scripts that land inside the workspace appear here. [Workflows](workflows.md#the-script-rule) has the rule and the two holes it closed.

It covers four things, and each of them either arrives with a repository or is written by something other than you: a workspace bundle, the workspace's own `.wisp.yaml`, an item's `orchestration.md`, and any hook script inside the workspace. For a bundle it hashes every file in the directory, not just the manifest that names them; for the two config files it hashes the file and, separately, each script the file names. The gate is on execution rather than configuration, so `branch:` and `worktree:` apply from any of them at once and only the keys naming something to run wait for an answer. [Workflows](workflows.md#what-the-gate-covers) is the whole of it.

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
| `WISP_PROGRAM` | Overrides `program:`, above every workflow layer including a bundle and an item. `wisp workflow` reports it by name in the `from` column rather than letting it arrive as if a file had set it. |
| `WISP_INSTALL` | **Any non-empty value** sets `install: true`, including `WISP_INSTALL=0`. |
| `XDG_CONFIG_HOME` | Relocates the user config, and with it `~/.config/wisp/workflows/`. The two are found together on purpose: a config file and the workflows it names must not be able to end up on opposite sides of this variable. |
| `EDITOR`, `VISUAL` | Read by `wisp workflow edit`, in that order, falling back to `vi`. Split on spaces rather than handed to a shell, so `code -w` works. |
| `TMUX` | Not wisp's, but read: it decides `switch-client` against `attach-session`, and gates `next`, `prev` and `hop`. |
| `TMPDIR` | Where the GitLab cache file goes, and the `source` hook's separate one. The source cache is keyed on the hook as well as the workspace, so changing `source:` gets a new file rather than the previous hook's rows. |
| `WISP_WORKSPACE` (again) | Also **set** by wisp, for every hook it runs, alongside a working directory of the workspace root. |

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
wisp workflow init solo          # a bundle, the built-in spelled out
wisp workflow use solo           # write workflow:, --here for this workspace
```

Both kinds of conflict are refused: a name already in use, and a path already registered under another name. Edits go through a YAML node, so comments survive.

The `.wisp.yaml` that `wisp ws new` writes is entirely commented out, and its first entry is `workflow:`, with a line saying what a bare name and a leading `./` mean and that `wisp workflow` reports what is in effect. The keys anyone will want are already there with the right spelling, which is otherwise a trip to this page.

Hand-editing stays fine. A path with no vault in it is reported as configured but missing rather than silently ignored: `wisp ws` says so, the workspace tree marks it `✗`, and `hop next` steps over it. Hopping to it by name still fails loudly, because you asked for that one specifically.

---

## The GitLab source

It is the **fallback**, used when the workflow in effect sets no `source` hook. A workflow that sets one displaces it entirely, and says nothing about having done so: `wisp workflow` is where that shows, as a `source` row naming a script and the layer it came from.

`gitlab.*` are still top-level config keys rather than options of a bundled source, which is the one place the workflow rework is not finished. A `source` hook shipped in a bundle carries its own configuration however it likes; the one tracker wisp knows about is still spelled differently from every other. The TTL is the one key that has come out of that group: `cache_ttl_min` is top-level and source-neutral now, because it was never a fact about GitLab. `gitlab.cache_ttl_min` remains as its older spelling.

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

The raw query response is cached at `$TMPDIR/wisp-gitlab-<hash of workspace path>.json`, refreshed when older than `cache_ttl_min`. It is namespaced by workspace because the bash version used one fixed filename, and pointing `WISP_WORKSPACE` at a second tree served it the first tree's items. A `source` hook's cache sits beside it under `wisp-source-<hash>.jsonl`, hashed over the workspace **and the hook**, so changing `source:` does not serve the previous hook's rows until the TTL runs out.

`ctrl-r` re-queries and replaces the cache only if an answer arrives. It no longer deletes the file first: deleting up front meant a `ctrl-r` against a source that had since broken emptied the list outright.

A refresh that fails says so in the status line and keeps serving the rows it already had, so a source that breaks annotates the list rather than emptying it. Local rows are untouched. [Hooks](hooks.md#failure) has the detail.

The query asks for open work items **assigned to you**, across the group and its descendants, capped at the first 100. That cap is silent: if you are the assignee on more than a hundred open items, the rest do not appear and nothing says so.

Being the assignee is what fills that list. It is not what decides whether you can open something — pasting a link works regardless.
