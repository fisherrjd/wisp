# Workflows

A workflow is a directory. It is what turns a handful of loose config keys into something you can name, copy, version and hand to someone else.

wisp used to compile one workflow in: branch `feature/<slug>`, one agent window at the workspace root, a shell per worktree, a briefing naming two files in an order somebody chose, and a needs-input signal grepped out of Claude Code's permission dialog. Every one of those is a preference wearing the clothes of a fact. They now live in a bundle, and the bundle wisp ships is one of the ones you can replace.

Status: **the bundle format and resolution are built. The hooks and the layout are resolved but not yet run.** [Where this stands](#where-this-stands) says exactly which is which. [Hooks](hooks.md) holds the reasoning that picked the hook shape.

---

## The bundle

```
~/.config/wisp/workflows/solo/
├── workflow.yaml
├── source.sh
├── context.sh
├── close.sh
└── provision.sh
```

`workflow.yaml` is the manifest. Everything else in the directory is whatever the manifest points at.

```yaml
# workflow.yaml
name: solo
description: one agent at the workspace root, a shell per worktree

program: claude
branch: "feature/{slug}"
worktree: "{repo}--{slug}"

hooks:
    source: source.sh
    context: context.sh
    close: close.sh
    provision: provision.sh

layout:
    - window: agent
      cwd: workspace
      run: "{program} {prompt}"
      focus: true
    - window: "{repo}"
      for: each-worktree
      cwd: worktree
    - window: provision
      when: provisioning
      cwd: workspace
      run: "{wisp} provision {item}"

status:
    needs_input: "No, and tell Claude what to do differently"
```

**A relative script path in a bundle resolves against the bundle directory.** That single rule is what makes a bundle self-contained: copying the folder copies the workflow, and there are no absolute paths inside it to fix up afterwards. The same key written in a config file instead resolves against the workspace root, because that is what a path in `.wisp.yaml` has always meant.

Keeping a workflow in git needs nothing from wisp. A workflow is a directory, so `git clone` is the install command and a symlink is the update mechanism: point `~/.config/wisp/workflows/team` at a checkout and `git pull` propagates.

### Where bundles live

| | |
|---|---|
| yours | beside the user config, `~/.config/wisp/workflows/<name>/` |
| a workspace's | `<workspace>/.wisp/workflows/<name>/` |
| the built-in | compiled into the binary, no directory anywhere |

Yours are found beside `config.yaml` rather than at a fixed path, so `XDG_CONFIG_HOME` relocates both together. A config file and the workflows it names should not be able to end up on opposite sides of that variable.

---

## Addressing

**There is no search path.** The shape of the value says where the workflow lives.

| written | means | lives at |
|---|---|---|
| `workflow: solo` | one of yours | `~/.config/wisp/workflows/solo/` |
| `workflow: ./team-gitlab` | this workspace's | `<workspace>/.wisp/workflows/team-gitlab/` |
| `workflow: default` | the built-in | compiled in |

An earlier draft had a search path with first-hit-wins, and it was wrong. It meant a repo shipping `.wisp/workflows/solo` would silently replace *your* `solo` in that workspace, with nothing anywhere saying so. Name collisions across a trust boundary are not a resolution problem to be ordered, they are an ambiguity to be deleted. A leading `./` costs one character and makes the config line legible without knowing wisp's rules.

An address is one path segment. Anything with a separator in it, or `.`, or `..`, is refused by name rather than followed, because the value arrives from a checked-in file and reaches the filesystem.

The one shadowing left is yours over the built-in: a directory of your own called `default` wins over the compiled-in one. That is allowed, because it is your directory and you meant it. Naming `default` when you have no such directory is simply the built-in, and is not annotated: it is the answer, not a failure to find one.

---

## Precedence

A bundle is a bag of defaults, not an all-or-nothing switch. Naming a key next to `workflow:` overrides just that key, at any layer, which is what makes "mostly my workflow, but this workspace tracks work in Jira" a one-line change rather than a forked directory.

Resolution happens in two passes, and the order matters.

**First, the address.** Four places may name a bundle, and the nearest one wins outright:

```
~/.config/wisp/config.yaml      workflow: solo
<workspace>/.wisp.yaml          workflow: team-gitlab
<item>/orchestration.md         workflow: review
wisp open <item> --workflow review
```

A bundle is *selected*, not merged with another bundle. There is no `extends:`, so there is never more than one bundle in the answer.

**Then the keys, per key, over five layers:**

```
1  built-in default              always complete, so every key has an answer
2  the named bundle              workflow.yaml
3  ~/.config/wisp/config.yaml    the default binding for this host
4  <workspace>/.wisp.yaml        the workspace binds
5  <item>/orchestration.md       the item overrides
   WISP_PROGRAM                  the agent command, above all of it
```

Layers 1 to 4 are the config merge that already exists ([Configuration](configuration.md)); layer 5 is the item, and it is the subject of the next section.

Two consequences worth knowing before they surprise you:

- **The bundle sits below both config files, wherever it was named from.** A `program:` in your user config beats a `program:` in a bundle the *item* asked for. That is deliberate: the bundle is the set of defaults you selected, and a key you wrote out by hand is the thing you wrote out by hand.
- **Empty means "said nothing", not "said empty".** There is no way to unset a key back to nothing, because treating absence as an override would make every partial bundle wipe the built-in, which is the exact opposite of the point.

`layout:` is the one key that overlays whole rather than per entry. A layer either supplies a list of windows or says nothing about them; two layouts are not merged window by window, because there is no name-matching rule for that which is not a surprise waiting to happen.

Hooks may be written nested under `hooks:` or flat, in a config file or an item:

```yaml
# <workspace>/.wisp.yaml
workflow: solo
context: .wisp/house-briefing.sh    # or: hooks: {context: ...}
```

Both are read and the nested one wins if somebody writes both. A bundle's `workflow.yaml` takes the nested form only: inside a bundle there is no ambiguity to resolve and one spelling is enough.

`WISP_PROGRAM` is applied after everything, as it always has been, and reports itself as the source when you ask where `program` came from.

### The one-shot flag

```
wisp open wisp/1042-retry --workflow review
```

Above every configured layer, and **written nowhere**. It is a flag rather than a file precisely because a one-off should leave nothing behind. It does not change what the workspace is bound to and it does not survive the session being killed and reopened.

---

## The item override

```yaml
# working_items/wisp/1042-retry/orchestration.md
---
repos:
    - repo: wisp
      branch: feature/1042-retry
workflow: review        # a whole workflow
program: aider          # or a single key
---
```

Two mechanisms, deliberately not one. The frontmatter says "this item is always a review item". The flag says "just this once". Conflating them produces either a flag that silently persists or a file edit for a one-off.

**The override belongs to the item, not to the session.** A session has nowhere durable to keep one: session state lives in tmux user options, which die with the tmux server, and an override you set once and lose on reboot is worse than not having it at all. The item folder is where per-item intent already lives, beside the repos and the branches, and items outlive sessions by design ([Items](items.md)).

### An item may not override `source`

Not a policy call, a bootstrapping impossibility. `source` decides where items come from, so an item cannot have an opinion about it: the item does not exist until `source` has run. Writing one is not an error, it is dropped and named:

```
--- an item cannot set `source`: it decides which items exist, and this one does not yet
```

`source` is therefore the only workspace-only key. Everything else acts on an item, which is exactly why an item may have a say.

### `branch:` and `repos[].branch` do not fight

A manifest's `repos[].branch` is explicit and per-repo. A workflow's `branch:` is a template. The manifest only asks the workflow for a branch when the entry left it empty, so an explicit branch always wins. This is worth writing down because the next person to read both keys will assume otherwise.

---

## The keys

| key | default | what it controls |
|---|---|---|
| `name` | `default` | The workflow's own name. Bundle only. |
| `description` | `one agent at the workspace root, a shell per worktree` | One line, for listings. Bundle only. |
| `program` | `claude` | The command in the agent window. wisp appends the context prompt as one shell-quoted argument, so flags belong here. |
| `branch` | `feature/{slug}` | The branch an item's repo gets when the manifest does not name one. |
| `worktree` | `{repo}--{slug}` | The directory name inside `worktrees:` holding one repo's checkout for one item. |
| `hooks.provision` | `.claude/scripts/provision-worktree.sh` | The script that builds a worktree. The one place wisp has always run code somebody else wrote. |
| `hooks.source` | none | Where items come from. See [Hooks](hooks.md). |
| `hooks.context` | none | What the agent is told. See [Hooks](hooks.md). |
| `hooks.close` | none | What closing an item out does. See [Hooks](hooks.md). |
| `layout` | one `agent`, one per worktree, one `provision` | The session's tmux windows. |
| `status.needs_input` | Claude Code's permission dialog line | The pane text that means an agent is waiting on a human. |

`worktree:` names a directory, not a path: a value containing a `/` is annotated and a value that expands to nothing, to `.` or to `..` falls back to `<repo>--<slug>` rather than writing a checkout somewhere `worktrees:` does not reach.

### Templates

The vocabulary is deliberately tiny and closed:

```
{item} {slug} {repo} {branch} {base} {worktree} {workspace} {program} {prompt} {wisp}
```

Plain substitution and nothing else. Anything wanting more than substitution should be a script, which is the whole point of the hooks: a config language that grows conditionals has become a bad programming language.

Not every token is in scope everywhere. `branch:` and `worktree:` are expanded per repo of an item, so they see `{item}`, `{slug}` and `{repo}`; the rest belong to `layout:`, which is expanded per window. An unknown token is left standing rather than blanked, so a typo shows up in the branch name instead of quietly producing `feature/`.

### `layout`

```yaml
layout:
    - window: agent
      cwd: workspace
      run: "{program} {prompt}"
      focus: true
```

| field | values | |
|---|---|---|
| `window` | templated | The window name. Truncated to 12 characters, as tmux window names have always been here. |
| `cwd` | `workspace`, `worktree`, `home` | Where the window starts. `worktree` only means anything with `for:` set. |
| `run` | templated | The command line, handed to `/bin/sh`. Empty leaves an interactive shell. |
| `for` | `each-worktree` | One window per repo in the manifest, instead of one window. |
| `when` | `provisioning` | Only when a worktree is still missing. |
| `focus` | bool | Select this window once the session is built. The first one wins. |

`for:` and `when:` are the only control flow there is, for the reason above.

### `status`

`status.needs_input` is small and matters more than its size. wisp used to match a literal string out of Claude Code's permission dialog, so anyone driving aider, codex or a bare shell had a `?` state that could never fire. One string per workflow fixes it.

It is resolved at the **workspace** level rather than per item, because the pane scan runs over every live session at once on every repaint, and resolving a workflow per row would read three files per row to answer a question that is the same for almost all of them.

An empty marker means the workflow has no needs-input signal. That is a live session, never an error.

---

## Accepting a workspace's workflow

A workspace's `.wisp.yaml` is checked in, so `git clone && wisp open` would run scripts out of the repo. That is already true of `provision:` today, so this makes an existing exposure bigger rather than creating a new one, which is why the answer is trust-on-first-use rather than a refusal.

The first time wisp is asked for a `./`-addressed workflow it is not loaded. The workspace's keys fall back to the built-in and every command touching the workspace says so:

```
--- workflow "./team-gitlab" is supplied by this workspace and has not been accepted
```

Accepting records a SHA-256 of that workflow's `workflow.yaml` in the **user config**, under `accepted:`, keyed by the workspace path and the address together:

```yaml
# ~/.config/wisp/config.yaml
accepted:
    /Users/you/work ./team-gitlab: 9f2c...
```

Three properties fall out of that, each because the alternative fails silently:

- **The record lives in the user config and nowhere else**, and is restored after the workspace file is merged. A workspace that could write its own acceptance would be accepting itself. This is the same protection `workspaces:`, `hosts:` and `default:` get, for a sharper reason ([Configuration](configuration.md)).
- **The hash is re-checked on every load**, not once. Accepting once must not be a standing permission for whatever the file becomes later: a `git pull` that rewrites `workflow.yaml` is exactly the moment worth asking about again, and it is the moment nobody would notice by hand.
- **The key is the workspace and the address together.** The same relative address in two workspaces is two different directories, and has to be accepted twice.

It is the manifest that is hashed rather than the whole directory. The manifest is what names every script, so a change to it is the change worth re-asking about; hashing the scripts as well would re-prompt on every edit to your own workflow and train you to say yes.

Only a `./` address needs this. Your own bundles are yours, and the built-in is the binary.

---

## When a workflow is broken

**A workflow that will not load costs you its keys, not your session.** Every failure below is a note rather than an error: notes are logged to stderr on `wisp open`, and to the provision window when provisioning is running.

| what | what happens |
|---|---|
| `workflow: typo`, no such directory | the built-in, with the address and the path it looked at |
| a `workflow.yaml` that will not parse | the built-in, with the parse error |
| a `./` workflow not yet accepted | the built-in, with the address to accept |
| `worktree:` with a `/` in it | named, and the checkout path falls back |
| a layout entry with no `window:` | that entry skipped, by position |
| unknown `for:`, `when:` or `cwd:` | that entry skipped, named |
| a window name used twice | the second one skipped |
| nothing left in `layout:` after all that | the whole key falls back to the built-in layout |
| an item setting `source:` | dropped, named |

This is what makes the built-in complete rather than merely first. Every key always has an answer, so a malformed bundle degrades one key at a time and you get a plainer session rather than a dead workspace.

---

## The command surface

Today, one flag:

```
wisp open <item> --workflow <name>     open this one differently, just this once
```

Everything else is files. `workflow:` goes in `~/.config/wisp/config.yaml` or `<workspace>/.wisp.yaml`, a bundle is a directory you create with `mkdir`, and an item's override is frontmatter in its `orchestration.md`. All of that is hand-editable and always will be.

The commands that would make it less so are designed and not built:

| command | would do |
|---|---|
| `wisp workflow [<item>]` | what resolved here, per key, with provenance |
| `wisp workflow list` | everything addressable from here, with source and status |
| `wisp workflow init <name>` | copy the commented built-in out as a starting point |
| `wisp workflow edit [<name>]` | `$EDITOR` on `workflow.yaml`, validated on save |
| `wisp workflow use <name>` | set `workflow:`, `--here` for this workspace |
| `wisp workflow show <name>` | resolved values for any workflow, and any errors in it |
| `wisp workflow accept <name>` | record the hash of a workspace-supplied workflow |

`wisp workflow` takes an optional item because with the item layer in place the resolved values are only fully answerable per item. The provenance column is the point of the whole command: "why did my session open like that" is currently unanswerable without reading Go, which is a bad answer for a tool whose entire subject is that the answer used to be hardcoded.

`wisp workflow accept` is named in the message wisp prints for an unaccepted workspace workflow, and until it exists the hash has to be written into `accepted:` by hand. That is stated rather than hidden, because a message naming a command that does not run is worse than no message.

---

## Moving one between machines

**A workflow does not cross a host boundary.** A remote workspace runs its own wisp, its own config load and its own `.wisp.yaml`, and nothing is shipped over ssh ([Remote workspaces](remote-workspaces.md)). A bundle in `~/.config/wisp/workflows/` on this laptop has no effect on a workspace that lives on another machine. You install it there too, the same way dotfiles get there.

That falls out of the existing design rather than being added to it, and it is the right way round: hooks configured here and run against a filesystem over there would be wrong in a way that is not obvious until it fails.

It also promotes the `./team-gitlab` form from a team-sharing convenience to something structural. **A workflow checked into the workspace is the only form that follows a workspace across hosts**, because it travels with the thing it is bound to.

---

## What this is not

| not building | because |
|---|---|
| `extends: other` | every unset key already falls back to the built-in, so inheritance from a *second* workflow is all this would add, and nobody has wanted it |
| version pinning | a pin is a copy that has stopped tracking the thing you edit, which is the exact problem reference-by-name exists to solve; per-key fallback already keeps the blast radius of a bad edit to one key |
| `wisp workflow add <url>` | `git clone` installs a directory and a symlink keeps it current; a package manager is a second distribution story to maintain |
| a Go plugin API | a hook is a program: writable in anything, testable by running it, debuggable by reading its output |

There is no cache and no install step. Config points at a directory by name, never at a copy taken at selection time, so editing a workflow reaches the next `wisp open` in any workspace on this host, and reaches no session that is already running. A session's windows and briefing are built once, at open, so an edit never reaches in and rearranges windows under an agent mid-task. Reopening a killed session is the way to adopt a change you just made.

---

## What must not become configurable

Restated here because a plugin system makes it tempting.

- **`Item.Key()`**, `<repo>/<iid>`. It is what makes the same ticket found in a session, a folder and a source into one row.
- **The four-word slug cut** applied to titles from a source. It matches what the bash version produced, so existing vault folders keep deduplicating against their remote counterparts.
- **The item name grammar**, `<parent>/<child>`, exactly two levels. A `source` hook is untrusted input producing item names, and every one of them still has to land inside the vault before anything is created. A workflow may decide where names come from; it may not decide what a name is allowed to be.

All three are identity rather than preference, and all three fail silently rather than loudly. A workflow that could change them could corrupt a vault by being slightly different. [Items](items.md) has the full argument.

---

## A four-line workflow

The smallest useful bundle sets one key. This one says the session is a single window and nothing else:

```yaml
layout:
    - window: agent
      cwd: workspace
      run: "{program} {prompt}"
```

`~/.config/wisp/workflows/quiet/workflow.yaml`, then `workflow: quiet` in either config file. There is no `name:`, no `hooks:`, no `program:` and no `branch:`. Everything it does not say falls back to the built-in, so branches are still `feature/{slug}`, worktrees are still `{repo}--{slug}`, provisioning still runs your script, and `?` still fires on Claude Code's permission dialog. What changes is that no shell windows are opened for the worktrees.

That is the whole shape of the thing: a workflow is a diff against the built-in, expressed as the keys you disagree about.

---

## Where this stands

| | |
|---|---|
| the bundle format and `workflow.yaml` | built |
| addressing, and the refusal of a search path | built |
| five-layer per-key resolution, and provenance | built |
| the item override, and the `source` refusal | built |
| `--workflow` on `wisp open` | built |
| accept-on-first-use, and the hash re-check | built |
| `program`, `branch`, `worktree`, `hooks.provision`, `status.needs_input` | built, and in use |
| `layout` | parsed and validated, not yet applied |
| `hooks.source`, `hooks.context`, `hooks.close` | resolved to a path, not yet run |
| the `wisp workflow` subcommands | not built |
| `workflow:` in the `wisp ws new` template | not built |

Layout resolving without being applied is invisible today, because the built-in layout describes exactly what the session builder does. That is the acceptance test for the whole first step: with no config at all, naming today's behaviour as a bundle changed nothing.

[Hooks](hooks.md) has the contract each of the three unbuilt hooks will have, and the reasoning that picked it.
