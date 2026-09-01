# Workflows

A workflow is a directory. It is what turns a handful of loose config keys into something you can name, copy, version and hand to someone else.

wisp used to compile one workflow in: branch `feature/<slug>`, one agent window at the workspace root, a shell per worktree, a briefing naming two files in an order somebody chose, and a needs-input signal grepped out of Claude Code's permission dialog. Every one of those is a preference wearing the clothes of a fact. They now live in a bundle, and the bundle wisp ships is one of the ones you can replace.

Status: **built.** The bundle format, the addressing, the five-layer resolution, the declarative layout, all four hooks and the `wisp workflow` commands are in and running. [Where this stands](#where-this-stands) lists what is done, and the five limitations that are real and known. [Hooks](hooks.md) holds the reasoning that picked the hook shape.

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

`workflow.yaml` is the manifest. Everything else in the directory is whatever the manifest points at, in whatever arrangement you like: `wisp workflow init` writes a manifest whose commented hook lines suggest `bin/`, and nothing enforces either shape.

`wisp workflow init solo` makes this directory and writes that manifest as the built-in spelled out, so none of it has to be typed from memory.

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

Layers 3 and 4 are the same two files, in the same order, as the config merge ([Configuration](configuration.md)), but they are read separately for this: workflow keys never become `Config` fields. That is why `program` and `provision` have no default in `Config` at all. Defaulting them in both places would make `""` mean "unset" and "claude" at once, and per-key resolution needs exactly that distinction. Layer 5 is the item, and it is the subject of the next section.

Two consequences worth knowing before they surprise you:

- **The bundle sits below both config files, wherever a file named it from.** A `program:` in your user config beats a `program:` in a bundle the *item* asked for. That is deliberate: the bundle is the set of defaults you selected, and a key you wrote out by hand is the thing you wrote out by hand. The one exception is a bundle named by `--workflow` on the command line, which goes on top instead; [the one-shot flag](#the-one-shot-flag) says why.
- **Empty means "said nothing", not "said empty".** There is no way to unset a key back to nothing, because treating absence as an override would make every partial bundle wipe the built-in, which is the exact opposite of the point.

`layout:` is the one key that overlays whole rather than per entry. A layer either supplies a list of windows or says nothing about them; two layouts are not merged window by window, because there is no name-matching rule for that which is not a surprise waiting to happen.

Hooks may be written nested under `hooks:` or flat, in a config file or an item:

```yaml
# <workspace>/.wisp.yaml
workflow: solo
context: .wisp/house-briefing.sh    # or: hooks: {context: ...}
```

Both are read and the nested one wins if somebody writes both, and writing both is now said rather than silently resolved:

```
  note: context is set both as `context:` and under `hooks:`; the one under hooks wins
```

Every other conflict in resolution already produced a note; this was the last silent precedence rule, and "the nested one wins" is not something anyone would guess from a file that sets both. A bundle's `workflow.yaml` takes the nested form only: inside a bundle there is no ambiguity to resolve and one spelling is enough.

`WISP_PROGRAM` is applied after everything, as it always has been, and reports itself as the source when you ask where `program` came from.

### The one-shot flag

```
wisp open wisp/1042-retry --workflow review
```

It picks the bundle, and it is **written to no file**. A flag rather than a file precisely because a one-off should leave nothing behind: it does not change what the workspace is bound to and it does not survive the session being killed and reopened.

A one-shot bundle goes **on top of both config files** rather than under them, which is the one place the ordering above is reversed. That is deliberate: a one-shot is an instruction rather than a default, and `wisp open x --workflow review` that still ran the `program:` out of `.wisp.yaml` would be doing most of what you asked and none of what you meant, with nothing on screen saying which half it kept. Internally the keys it supplied are attributed to `--workflow <name>`, though there is nowhere to see that: `wisp workflow` answers about the configured layers and takes no `--workflow` of its own, so a one-shot is the one resolution the provenance table cannot be asked about.

### How the background half learns about it

Opening an item with worktrees still missing builds a `provision` window that runs `wisp provision` as a **separate process**, and that process re-resolves the workflow from scratch. Told nothing, it resolves the written-down layers, misses the one-shot, and builds the worktree somewhere the session is not looking.

So the flag rides on a tmux session option, `@wisp_workflow`, set on the session before the provision window is created. `wisp provision` takes `--workflow` when it is given one and otherwise asks the session it is running in:

```
$ tmux show-option -qv -t wisp_ws_repoa-42-do-a-thing @wisp_workflow
hooked
```

tmux is where session state already lives, and dying with the tmux server is correct here: the flag's lifetime is the session's, which is exactly as long as "just this once" lasts.

**It used to be a `{flags}` template token** substituted into the provision window's `run:` line, and that was wrong for a reason worth recording. It made a correct one-shot depend on a hand-written layout containing a token nobody would think to include, and it failed silently when the token was absent: the session opened, the background half built the worktree under the wrong workflow, and nothing said so. `wisp workflow init` generated exactly such a layout, so the shipped starting point was itself the broken case. There is no `{flags}` token any more, and a hand-written `layout:` no longer has to know that any of this is happening.

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

### An item may not override `source` or `status.needs_input`

Two keys, refused the same way and for the same shape of reason: an item is not the thing that gets to answer either question.

`source` is a bootstrapping impossibility rather than a policy call. It decides where items come from, so an item cannot have an opinion about it: the item does not exist until `source` has run.

`status.needs_input` is resolved once for the whole workspace, because the pane scan runs over every live session at once on every repaint and per-row resolution would read three files per row ([`status`](#status)). An item's marker would therefore be reported and never consulted, which is worse than refusing it.

Writing either is not an error. It is dropped and named:

```
--- an item cannot set `source`: it decides which items exist, and this one does not yet
--- an item cannot set `status.needs_input`: the pane scan asks the workspace once, not each item
```

That is the `wisp open` form; `wisp workflow <item>` shows the same lines as `note:` under the table, which is the place to check a frontmatter edit before opening anything.

`status.needs_input` used to parse, display in the `from` column as coming from `orchestration.md`, and do nothing. Refusing it was the cheaper of the two honest fixes: the other was to resolve a workflow per picker row and pay for it on every repaint. Every other key an item writes is still an ordinary overlay and still wins.

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
| `window` | templated | The window name. Truncated to 12 characters, as tmux window names have always been here, and a template that expanded to nothing becomes `window`, since a window with no name is not creatable. |
| `cwd` | `workspace`, `worktree`, `home` | Where the window starts. `worktree` only means anything with `for:` set. |
| `run` | templated | The command line, handed to `/bin/sh`. Empty leaves an interactive shell. |
| `for` | `each-worktree` | One window per repo in the manifest, instead of one window. |
| `when` | `provisioning` | Only when a worktree is still missing. |
| `focus` | bool | Select this window once the session is built. The first one wins. |

`for:` and `when:` are the only control flow there is, for the reason above.

**The order of the list decides which window the session is built around.** tmux only makes a session's first window through `new-session`, so the first entry is the one that exists before anything else does, and `focus:` decides where the cursor lands afterwards rather than which window is first. That is the point of reading the order out of the layout instead of assuming window 0 is the agent. Focus is selected by name rather than index, because `base-index` may be 1 and `session:0` is then not reliably the first window.

A session's windows are built once, at open. Editing a layout never reaches into a running session and rearranges windows under an agent mid-task; reopening a killed session is how a change is adopted. The one exception is per-worktree windows, which the provision window adds when its checkouts land, skipping any name that is already there.

### `status`

`status.needs_input` is small and matters more than its size. wisp used to match a literal string out of Claude Code's permission dialog, so anyone driving aider, codex or a bare shell had a `?` state that could never fire. One string per workflow fixes it.

It is resolved at the **workspace** level rather than per item, because the pane scan runs over every live session at once on every repaint, and resolving a workflow per row would read three files per row to answer a question that is the same for almost all of them.

**So an item may not set it**, and is told so rather than left to find out. It used to be an ordinary overlay key: an item could write it, `wisp workflow <item>` would report it as coming from `orchestration.md`, and nothing would ever consult it, because the picker asks the workspace for one marker and scans every pane with it. There were two honest fixes, resolve per row and pay for it, or refuse the key the way `source` is refused. The second one shipped, for the cost the first one carries on every repaint. It is a `note:` under the table, so a frontmatter edit that will not fire is visible before a session is opened rather than after.

An empty marker means the workflow has no needs-input signal. Every session under it stays `●` rather than `?`. That is a live session, never an error.

---

## Accepting a workspace's workflow

A workspace's `.wisp.yaml` is checked in, so `git clone && wisp open` would run scripts out of the repo. That is already true of `provision:` today, so this makes an existing exposure bigger rather than creating a new one, which is why the answer is trust-on-first-use rather than a refusal.

The first time wisp is asked for a `./`-addressed workflow it is not loaded. The workspace's keys fall back to the built-in and every command touching the workspace says so:

```
  note: workflow "./ship" is supplied by this workspace and has not been accepted; run `wisp workflow accept ./ship` after reading it
```

That is the `wisp workflow` rendering. The same line arrives on `wisp open`'s stderr prefixed `--- ` instead, which is how every note wisp logs while opening a session is marked.

`wisp workflow list` marks the same thing in its own column, so the state is visible before a session is opened rather than only after:

```
$ wisp workflow list
  quiet                ~/.config/wisp/workflows
  ./ship               .wisp/workflows                            not yet accepted
* default              built-in
```

The `*` is on `default`, not on the bound-but-unaccepted `./ship`, and that is the point: the built-in is what is actually running. Marking the row that failed would say a workflow is in use when the note two lines above says nothing of it is.

`wisp workflow accept ./ship` prints the manifest in full, then the body of every script the manifest names, then asks. A hook naming a file that is not there yet is printed as `not there yet` rather than skipped, because that is still a file the workspace decides the contents of later. Scripts are never truncated: the interesting line in a script somebody else wrote is exactly as likely to be the last one as the first. `-y` answers yes without the prompt, and is required rather than assumed when stdin is not a terminal, because a prompt written to something that cannot answer is either a hang or a silent yes.

Accepting records a SHA-256 of that workflow's `workflow.yaml` in the **user config**, under `accepted:`, keyed by the workspace path and the address together:

```yaml
# ~/.config/wisp/config.yaml
accepted:
    /Users/you/work ./ship: 854659096926d77dbfe636122cf24cd7cc39e87426d7a46009cae5bbb6169f8f
```

Three properties fall out of that, each because the alternative fails silently:

- **The record lives in the user config and nowhere else**, and is restored after the workspace file is merged. A workspace that could write its own acceptance would be accepting itself. This is the same protection `workspaces:`, `hosts:` and `default:` get, for a sharper reason ([Configuration](configuration.md)).
- **The hash is re-checked on every load**, not once. Accepting once must not be a standing permission for whatever the file becomes later: a `git pull` that rewrites `workflow.yaml` is exactly the moment worth asking about again, and it is the moment nobody would notice by hand.
- **The key is the workspace and the address together.** The same relative address in two workspaces is two different directories, and has to be accepted twice.

It is the manifest that is hashed rather than the whole directory. The manifest is what names every script, so a change to it is the change worth re-asking about; hashing the scripts as well would re-prompt on every edit to your own workflow and train you to say yes.

**That is a real gap, not a technicality.** Accepting `./ship` shows you `bin/close.sh` and then records a hash of `workflow.yaml` alone. A later commit that rewrites `bin/close.sh` without touching the manifest is a script you have not read, running with the acceptance you gave to a different one. What is bought with that is a workflow you can iterate on without a prompt per save; what is paid is that the trust boundary is drawn around the list of scripts rather than around their contents. If that trade is wrong for a workspace, do not accept its workflow; the built-in is complete and costs a plainer session.

### What the gate does not cover, said plainly

**The accept gate is on `workflow: ./name` and on nothing else in the same file.** A checked-in `.wisp.yaml` that skips the bundle entirely and writes the keys out directly is not gated at all:

```yaml
# <workspace>/.wisp.yaml, checked in, and nothing asks about any of this
program: ./scripts/agent.sh
context: .wisp/context.sh
close: .wisp/close.sh
provision: .wisp/worktree.sh
layout:
    - window: agent
      run: "./scripts/whatever.sh"
```

Every one of those is a command out of the repo, resolved against the workspace root and run, with no prompt and no hash. So the honest description of the feature today is narrow: it stops a workspace **bundle** from running unread, and it does not make `git clone && wisp open` safe. That was already true of `provision:` before any of this existed, which is why the answer was trust-on-first-use for the new surface rather than a refusal for the old one, but a gate that covers one spelling of a thing and not the other is worth knowing about before it is relied on. `wisp workflow` is the command that shows every one of those keys and the file that set it, and reading it is the check that actually covers them.

Only a `./` address needs accepting. Your own bundles are yours, and the built-in is the binary.

---

## When a workflow is broken

**A workflow that will not load costs you its keys, not your session.** Every failure below is a note rather than an error: notes are logged to stderr on `wisp open`, to the provision window when provisioning is running, and printed under the table by `wisp workflow` and `wisp workflow show`, which is where you go looking for them on purpose. `wisp workflow edit` prints them too, straight after the editor exits.

| what | what happens |
|---|---|
| `workflow: typo`, no such directory | the built-in, with the address and the path it looked at |
| a `workflow.yaml` that will not parse | the built-in, with the parse error |
| a `./` workflow not yet accepted | the built-in, with the address to accept |
| **an unknown or misspelled key in `workflow.yaml`** | the rest of the file still loads, and the key is named |
| a hook set both flat and under `hooks:` | the nested one wins, and the collision is named |
| `worktree:` with a `/` in it | named, and the checkout path falls back |
| a layout entry with no `window:` | that entry skipped, by position |
| unknown `for:`, `when:` or `cwd:` | that entry skipped, named |
| a window name used twice | the second one skipped |
| nothing left in `layout:` after all that | the whole key falls back to the built-in layout |
| an item setting `source:` or `status.needs_input:` | dropped, named |

This is what makes the built-in complete rather than merely first. Every key always has an answer, so a malformed bundle degrades one key at a time and you get a plainer session rather than a dead workspace.

### A misspelled key is now said

The manifest is decoded strictly, and a key wisp does not know is reported:

```
  note: ~/.config/wisp/workflows/typo/workflow.yaml: yaml: unmarshal errors:
  line 3: field progam not found in type wisp.Workflow
```

This is the failure most worth catching in the whole file. A workflow that quietly does nothing is the worst thing `workflow.yaml` can be: you edit it, nothing changes, and there is no signal anywhere that you wrote `progam:`. Every other kind of broken bundle at least announces itself by behaving differently.

It is a note rather than a refusal, which takes two passes: the strict decode names the key, and the file is then re-read permissively so an unknown key costs you a line of prose rather than the whole bundle. The keys the file did spell correctly still apply.

It reaches every place a note is printed: `wisp workflow`, `wisp workflow show <name>`, and `wisp open`'s stderr. `wisp workflow edit` prints it the moment the editor exits, which is when a typo is cheapest to fix and while you still remember making it. A note is worth nothing where it is not printed, and the command whose whole job is explaining a bundle is the last place to be quiet about one.

Three things degrade **without** a note, because in each case there is an honest answer and nothing to report:

- `cwd: worktree` on a window with no `for:` starts at the workspace root. There is no checkout for that window to mean, and refusing the window would be worse than putting it somewhere you can work.
- A `for: each-worktree` window whose checkout does not exist yet is not created at open. The provision window adds it once the checkout lands, which is why that entry is separate from the rest of the layout.
- A layout that produces no windows at all, usually a layout of nothing but per-worktree entries with no worktrees yet, opens a single `agent` shell at the workspace root. A tmux session with no windows cannot exist, and an unexpected layout should still leave you somewhere you can work.

---

## The command surface

Everything here is still files. `workflow:` goes in `~/.config/wisp/config.yaml` or `<workspace>/.wisp.yaml`, a bundle is a directory, and an item's override is frontmatter in its `orchestration.md`. All of that is hand-editable and always will be. `wisp workflow` exists so that none of it has to be.

```
wisp workflow [<item>]      the workflow in effect, key by key, and where each key came from
wisp workflow list          every workflow addressable from here
wisp workflow show <name>   what one workflow sets, on its own
wisp workflow init <name> [--here]
                            write a starting point: the built-in, spelled out
wisp workflow use <name> [--here]
                            bind to a workflow
wisp workflow edit [<name>]
                            open its workflow.yaml in $EDITOR
wisp workflow accept ./<name> [-y]
                            read a workflow this workspace ships, and allow it to run
wisp workflow push <name> <host>
                            copy one of yours to another machine
wisp workflow list --host <host>
                            what is over there, and whether it matches here

wisp open <item> --workflow <name>
                            open this one differently, just this once
```

`--here` writes to the workspace instead of your user config, for `init` and `use` both.

### `wisp workflow`

The provenance column is the point of the whole thing. A value on its own says what wisp will do and says nothing about which of five files to edit to change it.

```
$ wisp workflow
default: one agent at the workspace root, a shell per worktree

  address   default
  selected  nothing names one, so the built-in
  lives in  compiled into wisp

  key         value                                      from
  program     claude                                     built-in
  branch      feature/{slug}                             built-in
  worktree    {repo}--{slug}                             built-in
  layout      3 windows: agent, {repo}, provision        built-in
  source      -                                          -
  context     -                                          -
  close       -                                          -
  provision   .claude/scripts/provision-worktree.sh      built-in
  needs_input No, and tell Claude what to do differently built-in

  agent        workspace  {program} {prompt}         [focus]
  {repo}       worktree   (a shell)                  [for each-worktree]
  provision    workspace  {wisp} provision {item}    [when provisioning]
```

The layout summary in the table names the windows and stops, which is enough to see that the layout changed and not enough to see how, so the detail follows underneath. A key nothing sets prints as `-` rather than as an empty column, so "wisp runs no close hook" is visibly an answer.

**Hook paths are printed as a person would like to read them**, which is not how wisp holds them. Every hook is resolved to an absolute path so it can be run from anywhere; printed that way, one hook put a hundred-character path in a nine-row table and pushed the `from` column, which is the column anybody ran this command for, off the side of every row. So a hook inside the workspace prints relative to it, one outside prints with `~` for your home directory, and anything still over 44 characters is cut **from the left**, because the identifying end of a path is its tail:

```
  key         value                                        from
  source      ~/.config/wisp/workflows/hooked/bin/items.sh hooked
  context     ...nfig/wisp/workflows/hooked/bin/context.sh hooked
  close       ~/.config/wisp/workflows/hooked/bin/close.sh hooked
```

The cut is applied before the column is measured, not instead of measuring it. Excluding a long value from the measurement kept the header narrow and then printed the long row anyway, which is the one thing worse than a wide column: eight rows that line up and one that does not.

It takes an **optional item** because with the item layer in place the resolved values are only fully answerable per item. Given one, the header says so and the `from` column names `orchestration.md` where the item had an opinion:

```
$ wisp workflow repoa/42-do-a-thing
ship: say what this one is for
for repoa/42-do-a-thing

  address   ./ship
  selected  .wisp.yaml
  lives in  /Users/you/work/.wisp/workflows/ship

  key         value                                      from
  program     aider                                      orchestration.md
  branch      feature/{slug}                             ./ship
  worktree    {repo}--{slug}                             ./ship
  layout      3 windows: agent, {repo}, provision        ./ship
  source      -                                          -
  context     -                                          -
  close       -                                          -
  provision   .claude/scripts/provision-worktree.sh      built-in
  needs_input No, and tell Claude what to do differently built-in

  agent        workspace  {program} {prompt}         [focus]
  {repo}       worktree   (a shell)                  [for each-worktree]
  provision    workspace  {wisp} provision {item}    [when provisioning]

  note: an item cannot set `source`: it decides which items exist, and this one does not yet
```

Anything that is not a known verb is taken as an item name, so a typo is refused as a missing item rather than silently answering the workspace question.

### `list`, `show`, `init`, `use`, `edit`

`list` shows what is **addressable**, not what is in effect. Two rows may share a name and be two different workflows, which is what the addresses are for. `*` marks the one in use, and the last column carries whatever is wrong with a row:

```
$ wisp workflow list
* quiet                ~/.config/wisp/workflows
  ./ship               .wisp/workflows                            not yet accepted
  default              built-in
```

A directory of your own named `default` is listed with `(shadows the built-in)` beside its path, because that is the one case where two rows genuinely collide.

`show <name>` resolves one address on its own: the built-in, overlaid by that bundle, and nothing else. Deliberately not the whole stack, since `wisp workflow` already answers "what is in effect here" and folding the config layers in here too would answer that question twice while leaving "what does this bundle actually set" unanswerable, which is the question you have when you are choosing between two of them. Its `selected` line reads `the command line`, since that is what named it. A `./` workflow that has not been accepted shows the built-in and the note, not its contents; `accept` is the command that prints those.

`init <name>` writes `workflow.yaml` as the built-in spelled out, with the reasoning beside each key, and it never writes over one that is there. An empty file would work just as well, since every key falls back on its own; it would also teach nothing, and the spelling of the keys is the part nobody can guess.

```
$ wisp workflow init quiet
~/.config/wisp/workflows/quiet/workflow.yaml

it is a copy of the built-in, so it changes nothing until you edit it:
  wisp workflow edit quiet
  wisp workflow use quiet
```

With `--here` it lands in `.wisp/workflows/` and the acceptance requirement is said at the moment the workflow is made, rather than left to surface as a note on the next session:

```
$ wisp workflow init ship --here
/Users/you/work/.wisp/workflows/ship/workflow.yaml

it is a copy of the built-in, so it changes nothing until you edit it:
  wisp workflow edit ./ship
  wisp workflow use ./ship

this one is the workspace's, so it also has to be accepted before it runs:
  wisp workflow accept ./ship
```

`use <name>` writes the `workflow:` key, through a YAML node so the comments in the file survive. The two destinations answer different questions: the user config is "this is how I work", inherited by every workspace that does not say otherwise, and `--here` is "this is how work happens here", travelling with the repos to everyone else who checks them out.

```
$ wisp workflow use ./ship --here
workflow: ./ship
  in /Users/you/work/.wisp.yaml

nothing of it runs until it has been read and accepted:
  wisp workflow accept ./ship

  wisp workflow   what that changed, key by key
```

`edit` opens `$EDITOR` (then `$VISUAL`, then `vi`) on the manifest and re-reads it afterwards. The re-read is the whole reason to go through wisp rather than the editor alone: a manifest that no longer parses costs its keys silently, and the moment to be told is while you still remember what you changed. `EDITOR` is split on spaces rather than handed to a shell, so `code -w` works. Asked to edit the built-in, it refuses and names the three commands that get you a copy of it instead, since there is no file to open.

A flag a subcommand does not take is **refused rather than ignored**, and that is because of `accept`: a mistyped `-Y` that silently means "no flag at all" is the difference between recording an acceptance and being asked about one, and those two must never be one keystroke apart.

### Remote workspaces refuse all of it

```
$ wisp -w box workflow
wisp: workspace "box" is on bigbox, and its workflows are files over there

ask the wisp that owns them:
  ssh bigbox wisp workflow
```

Every workflow is a file, and for a remote workspace every one of those files is on the other machine. Answering from here would describe this machine's config against that machine's paths, which is worse than not answering. This applies to the whole subcommand, `list` and `show` included, and it is not a gap to be filled later: the answer genuinely lives over there, and the message names the command that gets it.

---

## Moving one between machines

**A workflow does not cross a host boundary on its own.** A remote workspace runs its own wisp, its own config load and its own `.wisp.yaml`, and nothing is shipped over ssh ([Remote workspaces](remote-workspaces.md)). A bundle in `~/.config/wisp/workflows/` on this laptop has no effect on a workspace that lives on another machine.

That falls out of the existing design rather than being added to it, and it is the right way round: hooks configured here and run against a filesystem over there would be wrong in a way that is not obvious until it fails.

So across machines a workflow is necessarily a **copy**. The question is not how to avoid the copy, it is whether wisp makes one for you and tells you when it has drifted. It does both:

```
$ wisp workflow push hooked localhost
hooked to localhost:~/.config/wisp/workflows/hooked  (1 files, 317 B)

check it landed:
  wisp workflow list --host localhost
```

The `<host>` argument of `push` and of `list --host` is looked up in `hosts:` first and used as written when it is not there. So both spellings work: `wisp workflow push solo box` for a machine you have renamed (`hosts: {box: jade@eldo}`), and `wisp workflow push solo jade@eldo` for one you have not registered at all. Naming a machine is what `wisp host` tells you to do, so naming one of those had to work; refusing a raw ssh target would have been a rule nobody asked for.

It packs the directory with `tar` and sends it down the ssh connection wisp is already opening, rather than `scp` per file: a bundle is a handful of small files and the round trips cost more than the bytes. The destination is expanded by the **far side's** shell, `"${XDG_CONFIG_HOME:-$HOME/.config}"/wisp/workflows`, because XDG may be set over there and that is none of this machine's business. `push` is idempotent, so re-running it is how you resync rather than something to be careful about.

The count in that line is of files at the top of the bundle only. Everything under it is sent, so a bundle keeping its scripts in `bin/` reports fewer files than it moved. The tally is a sanity check on the transfer, not an inventory.

`list --host` is the drift check, and it has four answers:

```
$ wisp workflow list --host localhost
  hooked               differs
  onlythere            only there
  quiet                missing
```

`same`, `differs`, `missing` (yours, not there yet) and `only there` (theirs, not here). The comparison is a hash of `workflow.yaml`, for the same reason acceptance hashes it: the manifest is what names every script. It runs one `shasum`/`sha256sum` shell line over there rather than a wisp subcommand, so it still answers correctly against a machine running an older wisp that has never heard of workflows.

Two things `push` will not do. It refuses a `./` address, since a workspace's own workflow travels with the workspace and there is nothing to push. And it is never automatic on open: opening a remote item would then silently write into another machine's config directory, which is a surprising thing for a command called `open` to do, and it would fire on the non-interactive paths (`board --json`, the remote board fetches under `BatchMode=yes`) where nothing can ask you first.

None of this changes the fact that **a workflow checked into the workspace is the only form that follows a workspace across hosts**, because it travels with the thing it is bound to. `push` is for the other case: your own bundle, on every machine you work from.

---

## What this is not

| not building | because |
|---|---|
| `extends: other` | every unset key already falls back to the built-in, so inheritance from a *second* workflow is all this would add, and nobody has wanted it |
| version pinning | a pin is a copy that has stopped tracking the thing you edit, which is the exact problem reference-by-name exists to solve; per-key fallback already keeps the blast radius of a bad edit to one key |
| `wisp workflow add <url>` | `git clone` installs a directory and a symlink keeps it current; a package manager is a second distribution story to maintain |
| a Go plugin API | a hook is a program: writable in anything, testable by running it, debuggable by reading its output |

There is no cache and no install step on a machine. Config points at a directory by name, never at a copy taken at selection time, so editing a workflow reaches the next `wisp open` in any workspace on this host, and reaches no session that is already running. `wisp workflow push` is the one copy, and it exists only because a host boundary leaves no alternative; `list --host` is there so that copy cannot drift silently. A session's windows and briefing are built once, at open, so an edit never reaches in and rearranges windows under an agent mid-task. Reopening a killed session is the way to adopt a change you just made.

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

`~/.config/wisp/workflows/quiet/workflow.yaml`, then `workflow: quiet` in either config file. `wisp workflow init quiet` makes the directory and a manifest, and `wisp workflow use quiet` writes the binding; what init writes is the built-in in full, so getting to the four lines above means deleting the rest, which is the intended direction of travel. There is no `name:`, no `hooks:`, no `program:` and no `branch:`. Everything it does not say falls back to the built-in, so branches are still `feature/{slug}`, worktrees are still `{repo}--{slug}`, provisioning still runs your script, and `?` still fires on Claude Code's permission dialog. What changes is that no shell windows are opened for the worktrees. `wisp workflow` afterwards is the shape of the idea in one screen: `layout  1 window: agent  quiet`, five rows still reading `built-in`, and three hooks still reading `-`. Its header still says `default: one agent at the workspace root, a shell per worktree`, because a bundle with no `name:` and no `description:` sets neither, and the built-in's are what is left. Set `name:` if you would rather the header agreed with the address.

That is the whole shape of the thing: a workflow is a diff against the built-in, expressed as the keys you disagree about.

---

## Where this stands

All of it is built.

| | |
|---|---|
| the bundle format and `workflow.yaml` | built |
| addressing, and the refusal of a search path | built |
| five-layer per-key resolution, and provenance | built |
| the item override, and the `source` and `status.needs_input` refusals | built |
| `--workflow` on `wisp open`, carried to the provision window on `@wisp_workflow` | built |
| accept-on-first-use, and the hash re-check | built |
| `program`, `branch`, `worktree`, `hooks.provision`, `status.needs_input` | built, and in use |
| `layout` | built: it is what the session builder builds from |
| `hooks.source`, including `--url`, `hooks.context`, `hooks.close` | built, and run |
| the `wisp workflow` subcommands, `push` and `list --host` included | built |
| `workflow:` in the `wisp ws new` template | built |

The acceptance test for the whole thing was that with no config at all, naming today's behaviour as a bundle changed nothing: the built-in layout describes exactly what the session builder used to do by hand, so a workspace that says nothing gets the session it always got.

### The limitations that are real

Five, and none of them is a rough edge waiting on a rename. Each is a trade somebody made and could unmake.

- **Acceptance hashes `workflow.yaml` and nothing else.** A script the manifest names can be rewritten afterwards without wisp asking again: you are shown `bin/close.sh` and what gets recorded is a hash of the file that named it. See [Accepting a workspace's workflow](#accepting-a-workspaces-workflow) for what that buys and what it costs.
- **The accept gate covers `workflow: ./name` and not the same file's other keys.** A checked-in `.wisp.yaml` that writes `program:`, `context:`, `close:`, `provision:` or a `layout[].run` directly runs unprompted, and `provision:` has always done so. [What the gate does not cover](#what-the-gate-does-not-cover-said-plainly) is the whole of it, written out. This is the honest limit of the feature today: it stops a workspace *bundle* from running unread, not a workspace.
- **`gitlab.*` is still a set of top-level config keys, not a bundled source.** The built-in GitLab source is the fallback when no `source` hook is set, and it is configured where it always was, in `gitlab:` in either config file. A `source` hook shipped with a bundle carries its own configuration however it likes; the built-in one does not, so the one tracker wisp knows about is still spelled differently from every other. Its cache TTL is the one piece that has moved out: `cache_ttl_min` is a top-level, source-neutral key, and `gitlab.cache_ttl_min` is read as the older spelling of the same thing.
- **A hook gets 60 seconds and 8 MB, and neither number is yours to set.** `source:`, `context:` and `close:` are killed at a minute and reported as `gave up after 1m0s`; more than 8 MB on stdout ends the run rather than truncating it. The bound is generous because a `close` hook may be posting to a tracker, and it exists at all because these run where nothing can cancel them: a `source` hook that hangs takes the picker's refresh with it. `provision:` is outside both, deliberately, since it runs in a window of its own where a slow nix build is the normal case. [Hooks](hooks.md) has the detail, including where the deadline does not hold.
- **A remote workspace refuses `wisp workflow` entirely.** Not a gap: the files are on the other machine, and the command says which `ssh` line answers.

[Hooks](hooks.md) has the contract each hook has, and the reasoning that picked it.
