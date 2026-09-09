# Commands

Every command, every flag, and what each one prints when it fails.

`wisp <command> [args]`. There is one global flag, `-w`, and it must come first. Everything else is per-command.

Errors go to stderr with a `wisp: ` prefix on the first line and exit 1. Everything else exits 0. There are no other exit codes.

---

## The global flag

```
-w <workspace>          act on a named workspace instead of the one you are in
--workspace <workspace> the long form
```

It must lead. `wisp ls -w side` does not work: `-w` is only consumed at the head of the argument vector, and a trailing one is read as a positional argument by whichever command follows, or silently ignored. It is repeatable, and the last one wins.

The name may be a workspace under `workspaces:`, a machine under `hosts:` (meaning that machine's default workspace), or `host/name` for a machine holding more than one. Resolving it is pure syntax and costs no network call: the host comes from your config and the rest is the far side's own name for the workspace, which that machine will resolve itself.

Naming a workspace outright skips the upward directory search entirely. That is the point: searching from the current directory would find the workspace you are leaving.

Unknown names are refused with the list of what is known:

```
no workspace named "demo"

known: github, work, eldo
add a machine under `hosts:` or a workspace under `workspaces:` in /Users/you/.config/wisp/config.yaml
```

Two commands do not take it usefully. `wisp -w side version` and `wisp -w side help` both fail with `unknown command "version"`, because those two are handled before `-w` is stripped. `wisp ls` accepts it and then ignores it, since it always lists every workspace.

---

## Going somewhere

### `wisp`, `wisp home`

Go to this workspace's picker session, creating it if it is not there. Prints nothing: the process is replaced by tmux.

The home session is named `wisp-<workspace>` and holds one window running `wisp pick` in a loop. [Sessions](sessions.md#the-home-loop) explains why it is a loop.

```
cannot locate the wisp binary: <err>
could not create the wisp home session: <err>
```

### `wisp pick`

Run the picker once, here, without a session. This is what the home loop runs, and what you want in a script or a tmux popup. On exit it opens the chosen item, hops to the chosen workspace, or detaches.

### `wisp next`, `wisp prev`

Cycle to the next or previous item session **within this workspace**, sorted by session name, wrapping at both ends. The home session is not in the ring: it is a destination, not a stop. From a session that is not an item's, `next` enters at the head and `prev` at the tail.

With no sessions it returns quietly and prints nothing.

```
not inside tmux
could not switch to <session>: <err>
```

### `wisp hop [next|prev|<workspace>]`

Move to another workspace. The argument defaults to `next`. `+` and `-` are accepted as synonyms for `next` and `prev`. Naming the workspace you are already in is a no-op.

Hopping lands on the session you were last in over there, so a trip out and back is a round trip rather than a reset. With nothing recorded, it lands on that workspace's home.

The blind step walks the same list `wisp ws` shows, which means **one ssh round trip per configured machine**. Workspaces whose vault does not exist are stepped over rather than stranding you on them.

```
not inside tmux
only one workspace (work); add a machine under `hosts:` or a workspace under `workspaces:` in <path>
nowhere to hop: eldo, box are not reachable; `wisp ws` shows why
```

---

## Items

### `wisp open <item>`, `wisp o <item>`

Open an item directly, by its vault name. Creates the session if it does not exist, attaches if it does. [Sessions](sessions.md) covers exactly what gets built.

Unlike the picker, this can be handed a name that does not exist, so it checks first. There are three offline ways for an item to be real: the folder exists, a session is already running under that name, or its repo half is a directory in this workspace, which is what a GitLab item looks like before it is opened for the first time.

```
usage: wisp open <item>
"../x" does not name an item in working_items/
no item named "x"

Nothing in working_items/ has that name, no session is running under it, and it does not name
a repo in this workspace.

Fix by either:
  - create it:  wisp new x
  - pick from what is already there:  wisp
```

Progress lines (`--- provisioning payments-api (feature/…)`) go to stderr.

### `wisp new <name|url> [--json]`

Make an item. Three input shapes, told apart by the input itself rather than by a mode you have to select:

| you type | you get |
|---|---|
| `https://gitlab.example.com/grp/sub/repo/-/issues/42` | `repo/42-<slug-from-title>` |
| `repo/some-name` | `repo/some-name`, both halves slugified |
| `some name`, run inside `<workspace>/<repo>/` | `<repo>/some-name` |
| `some name`, in a workspace with one repo | `<repo>/some-name` |
| `some name`, otherwise | `_adhoc/some-name`, with a note on stderr naming the repos it could have gone under |
| anything, with a `new` hook set | whatever the hook answers, or the row above when it has no opinion ([Hooks](hooks.md#new-how-an-item-gets-its-name)) |
| `some name`, with `item.parent` set | `<parent>/some-name`, no inference, no note |

Only the first `/` splits, so the result is always exactly two levels. An item that already exists is handed back rather than refused, which is what makes `ctrl-n` on an existing name just open it.

A bare name **tries to land under a repo first**. `_adhoc` is the fallback for work nothing ties to a checkout, not the default for work you did not spell out: an item with a repo gets a worktree, a shell window and a hub note in its briefing, and one without gets a notes-only session. Standing inside a checkout when you run `wisp new` is as clear as typing the repo, and a workspace with one checkout has nothing to choose between. With several repos and no cwd to go on, wisp files under `_adhoc` and says so rather than guessing, because a wrong repo gets a worktree built for work that was never about it and a missing one is a rename away. The picker asks instead of guessing ([The picker](picker.md)).

A pasted link **does not have to be assigned to you**. Being the assignee is what fills the `+` section of the picker; it has never had anything to do with whether you can open something.

`--json` prints `{"name":"_adhoc/thing"}` instead of the bare name, plus a `note` field when the name fell back to `_adhoc` with repos available. That form is how one wisp asks another to create an item on the machine that owns the workspace.

```
nothing to create
"x/" does not name an item; give it a repo and a name, like `wisp/my-thing`
set gitlab.repo_pattern in .wisp.yaml before pasting links
gitlab.repo_pattern: <regexp error>
no repo in that URL; gitlab.repo_pattern did not match it
no item number in that URL
no project path in that URL
glab not on PATH, so #42 cannot be named from its link; type a name instead
could not reach gitlab to name #42: <err>
gitlab has no #42 in grp/sub/repo, or your token cannot see it
unreadable gitlab response: <err>
<host>: could not read the created item back
```

### `wisp done <item> [-m <line>] [--undo]`, `wisp done --list`

Close an item out. It leaves the picker; nothing on disk is removed.

Two things happen together: `done: true` goes into the item's `notes.md` frontmatter, and `-m` writes a line into the body under a dated heading.

They happen together because separately they did not happen at all. The flag was one keystroke and the write-up was a trip to an editor, so the items that got closed out and the items that got written up turned out to be **disjoint sets**: every item carrying the flag had a note holding nothing but the flag, while the one item with a real closing note was never marked.

So closing out an item whose note is still empty is refused, and the message says both ways forward:

```
nothing is written down about repo/1042-retry-backoff

A closed-out item is one you stop seeing, so the note is the only thing left
of it. Say what happened:
  wisp done repo/1042-retry-backoff -m 'what it turned out to be'

Or close it out bare, for work there is nothing to say about:
  wisp done repo/1042-retry-backoff --anyway
```

An item you have already taken notes on closes with no ceremony. The friction lands exactly where the record would otherwise be lost, and never anywhere else. The stub `# <slug>` heading wisp writes with the folder does not count as having said anything.

`--undo` reverses the flag and leaves whatever was written. `--list` prints the names of everything closed out, one per line.

The note is edited as a YAML node rather than parsed into a struct and re-emitted, so tags, aliases and anything else your editor keeps in that frontmatter survive untouched. The write goes through a temp file in the same directory and a rename: this is the one file in an item nobody else can reconstruct.

An item with a live session keeps its row regardless. Something is running under that name, and hiding it would leave an agent on the machine with nothing pointing at it.

```
usage: wisp done <item> [-m <line>] [--undo] | wisp done --list
nothing is written down about <item>
<item> has no notes.md to mark
<path>: frontmatter is not a mapping
```

This one is local-only. `wisp -w eldo done x` fails on a path that does not exist here.

### `wisp kill <item>`

Kill an item's session. Prints `killed <item>`.

For a remote item this kills the session **on the machine doing the work**, then the local wrapper. Dropping the wrapper alone would leave an agent running that nothing lists any more. Walking away and leaving it running is what `esc` already does, so kill has to mean the heavier thing or there is no way to say it.

Worktrees, branches, the vault folder and every note survive.

### `wisp kill --all [--everywhere] [-y]`

Every session in this workspace, or in every workspace with `--everywhere`, listed with its glyph and then asked about, because every live agent loses its context. `-y` skips the question and is required when there is no terminal to ask on, the same rule `accept` follows. The session the command runs in is never on the list: killing it would take the terminal asking the question with it. Each item's kill hook runs, through the workspace that owns it.

```
$ wisp kill --all
3 sessions in github:
  ● _adhoc/wisp-workflow-creation
  ? wisp/1042-retry-backoff
  ● wisp/1050-docs

kill them? [y/N] y
killed 3
```

Not a refresh: a session is tmux plus the agent in it, and the wisp binary is only involved at open, at kill and in the picker's loop, which picks a new binary up on its next pass. The reason to end every session is to start the agents fresh.

```
usage: wisp kill <item> | wisp kill --all [--everywhere] [-y]
no session for <item>
could not kill <session>: <err>
no sessions to kill in <workspace>
nothing killed
<n> would not go
```

### `wisp provision <item> [--workflow <name>]`

Build any worktrees the item's manifest declares and rewrite its context file. wisp runs this itself, in a side window, whenever a session opens with worktrees still missing; it is also useful by hand after deleting one.

`--workflow` exists because this is a **separate process** from the `wisp open` that started it, and it resolves the workflow again from scratch. Told nothing, it would resolve the written-down layers, miss a one-shot `--workflow` the session was opened with, and build the worktree somewhere the session is not looking.

Given no `--workflow`, it asks the session instead: `wisp open --workflow` records the address on the tmux session option `@wisp_workflow` before the provision window is created, and this reads it back. That is why the built-in layout's provision line is a plain `{wisp} provision {item}` with no flag in it, and why a hand-written `layout:` gets this right without having to know it was a question.

It **exits 0 even when it fails**, printing the error and waiting for a keypress instead. That is deliberate: it runs in a tmux window that closes the moment its command returns, and a failure that vanishes is one nobody can read.

With no script in effect it builds the worktree itself with git ([Sessions](sessions.md#provisioning)); with one, it runs the script.

```
usage: wisp provision <item> [--workflow <name>]
provisioning script missing: <path>
provision names <path>, which wisp did not run (see the notes above), so <repo> was not built

run `wisp workflow accept` after reading it, or drop `provision:` to let wisp build worktrees itself
```

---

## Listing

### `wisp ls`

Every wisp session on the tmux server, across **all** workspaces. Columns are workspace, item, tmux session name. Sessions predating workspaces carry no workspace tag and are attributed to the default one.

No tmux server means no output and exit 0, not an error.

### `wisp repos`

The workspace's repo checkouts: immediate subdirectories holding a `.git`, minus the vault, which is a git repo too but is not a code repo. One per line.

### `wisp ws`

Every workspace in the ring, with a `*` on the one you are in. Probes every configured machine and every remote workspace in parallel, so it costs one ssh round trip per machine.

```
* work         /Users/you/work                       3 live, 1 waiting
  side         /Users/you/projects/side              0 live, 0 waiting
  eldo         jade@eldo:/home/jade/work             2 live, 0 waiting
  ghost        /Users/you/gone                       does not exist yet
  box          jade@box                              cannot reach jade@box: ssh: connect to host box port 22: Operation timed out
```

### `wisp host`, `wisp hosts`

The machines wisp can reach, name and ssh target, sorted. No output if none are configured.

---

## Editing the config

Everything here writes to `~/.config/wisp/config.yaml` through a YAML node, so your comments survive. A brand-new section is appended as plain text rather than the whole file being re-emitted.

### `wisp ws new [-p|--parents] [--workflow <name>] <name> [path]`

Make a directory a workspace and register it. The path defaults to `.`, and may be local or `host:path`. `-p` creates the directory too, and is accepted in any position: a trailing `-p` should not become a directory named `-p`. `--workflow` binds the new workspace to a bundle at birth, writing `workflow:` into its `.wisp.yaml` exactly as `wisp workflow use --here` would.

It creates the vault and a fully commented `.wisp.yaml` to fill in, then registers the name, adding `default:` if nothing already answers that question. Run on a directory that is already a workspace, it just registers it, which is how you make an existing vault reachable by `hop`.

Without `-p` the directory has to exist. A mistyped path should fail there and then rather than become a workspace somewhere nobody meant to put one, where the mistake only surfaces later as a picker with nothing in it.

```
usage: wisp ws new [-p] [--workflow <name>] <name> [path]
a workspace needs a name: wisp ws new <name> [path]
bigbox is a machine, not a workspace on one

  wisp host add <name> bigbox

gets you every workspace it holds
<path> does not exist

wisp adds a workspace to a directory you already have, so a mistyped path
fails here rather than becoming a workspace somewhere you never meant.
To create it anyway:
  wisp ws new -p <name> <path>
<path> exists but is not a directory
workspace "side" already points at ~/projects/side
~/projects/side is already the workspace "old"
could not create it on <host>: <err>
cannot locate a config directory to record this in
```

On success it tells you what to do next, and warns about the one arrangement that surprises people:

```
note: this sits inside the workspace at /Users/you/work.
Running wisp anywhere below here will find this one, not that one.
```

### `wisp ws rm <name>`, `wisp ws forget <name>`

Forget a workspace. Prints `forgot <name>; nothing on disk was touched`, which is the whole story: it edits the config and leaves every file and every session alone. Forgetting and destroying must not be the same keystroke, which is why there is no flag here to make it the second thing.

```
usage: wisp ws rm <name>
eldo/side belongs to the machine "eldo"; forget the machine to drop all of its workspaces
no workspace named "x"
"work" is the workspace you are in; hop somewhere else first
```

### `wisp host add [name] <ssh target>`

Register a machine. Every workspace it holds joins the ring. With one argument the name is derived from the target: `jade@eldo.local` becomes `eldo`, which is what anyone would call it.

A machine that does not answer is **still added**. It is almost always a typo, but it is also sometimes a desktop that is asleep, and forgetting one is a single keystroke.

```
usage: wisp host add [name] <ssh target>
a machine needs a name: wisp host add <name> <ssh target>
machine "eldo" already points at jade@eldo
jade@eldo is already the machine "box"
```

### `wisp host rm <name>`, `wisp host forget <name>`

Forget a machine and everything it holds, in one go. Same message, same guarantee.

Anything else after `host` is refused with its own usage block. (`wisp ws <unrecognised>` currently falls through to the plain listing instead, which is an inconsistency rather than a design.)

---

## Workflows

`wisp workflow` and everything under it. A workflow is resolved per key across five layers, which is what makes a four-line workflow useful and also what makes "why did my session open like that" unanswerable from any one file; these subcommands are where that is readable and editable. [Workflows](workflows.md) is the reference for the bundle format, the addressing rule and the precedence.

`wisp wf` is an alias for the whole thing.

**A remote workspace refuses all of it**, before any argument is looked at:

```
$ wisp -w bigbox workflow
wisp: workspace "bigbox" is on jade@bigbox, and its workflows are files over there

ask the wisp that owns them:
  ssh jade@bigbox wisp workflow
```

Every workflow is a file, and for a remote workspace every one of those files is on the other machine. Answering from here would describe this machine's config against that machine's paths, which is worse than not answering.

A flag a subcommand does not take is **refused rather than ignored**. That is because of `accept`: a mistyped `-Y` that silently means "no flag at all" is the difference between recording an acceptance and being asked about one, and those two must never be one keystroke apart.

```
unknown flag "-Y"

<the usage block>
```

### `wisp workflow [<item>]`

The workflow in effect, key by key, with the file that supplied each key beside it. The third column is the point of the command: a value on its own says what wisp will do and says nothing about which of five files to edit to change it.

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

The header line names the workflow only when a bundle named itself. When nothing names one and a config file has simply taken keys off the built-in, it says so instead, because the built-in's description stops being true of what runs the moment `program:` comes from somewhere else:

```
default: the built-in, with program, layout overridden
```

A key nothing sets prints as `-`, so "wisp runs no close hook" is visibly an answer rather than an empty column. Hook paths print relative to the workspace, or with `~` for your home directory, and are cut from the left at 44 characters so the `from` column stays on screen. Non-fatal problems print as `note:` lines under the layout detail.

The optional item argument is the per-item answer, which is the one worth having: an item may override keys in its own `orchestration.md`, so "why did *this one* open like that" is a different question from "what does this workspace do". Anything that is not a known verb is taken as an item name, so a typo is refused as a missing item rather than silently answering the workspace question.

```
no item named "nosuch"

Nothing in working_items/ has that name, no session is running under it, and it does not name
a repo in this workspace.
...

<the usage block>
```

### `wisp workflow list [--host <host>]`

What is **addressable** from here, not what is in effect. Two rows may share a name and be two different workflows, which is exactly what the addresses are for. `*` marks the one actually in use, and the last column carries whatever is wrong with a row.

```
$ wisp workflow list
  quiet                ~/.config/wisp/workflows
  ./ship               .wisp/workflows                            not yet accepted
* default              built-in
```

The `*` follows what is running, not what is bound: a bound workflow that will not load leaves the built-in in use, and marking the failed row would contradict the note printed beside it. A directory of your own named `default` is listed with `(shadows the built-in)` beside its path.

`--host <host>` compares this machine's bundles with another's, over a `shasum` of each `workflow.yaml`. Four answers: `same`, `differs`, `missing` (yours, not there yet) and `only there`.

```
$ wisp workflow list --host localhost
  notthere             missing
  quiet                differs
```

The host is looked up in `hosts:` first and handed to `ssh` as written when it is not there, so a name you registered and a raw ssh target both work. The comparison runs one `shasum`/`sha256sum` shell line over there rather than a wisp subcommand, so it still answers correctly against a machine running a wisp too old to have heard of workflows.

```
usage: wisp workflow list --host <host>

`wisp host` is what there is to name
cannot reach nosuchhost: ssh: Could not resolve hostname nosuchhost: nodename nor servname provided, or not known
neither <host> nor this machine has any workflows of its own
```

### `wisp workflow show <name>`

What one workflow sets, on its own: the built-in overlaid by that bundle, and nothing else. Deliberately not the whole stack, since `wisp workflow` already answers "what is in effect here" and folding the config layers in here as well would answer that twice while leaving "what does this bundle actually set" unanswerable. Its `selected` line reads `the command line`.

A `./` workflow that has not been accepted shows the built-in and the note, not its contents. `accept` is the command that prints those.

Notes print under the table here as they do everywhere else, including the one naming a key the manifest misspelled:

```
  note: ~/.config/wisp/workflows/typo/workflow.yaml: yaml: unmarshal errors:
  line 3: field progam not found in type wisp.Workflow
```

```
usage: wisp workflow show <name>

`wisp workflow list` is what there is to name
```

### `wisp workflow init <name> [--from <shipped>] [--here]`

Write a starting point: a copy of one of the bundles wisp ships, `default` unless `--from` names another, with `name:` rewritten and every comment kept. It never writes over one that is there, because a workflow already there is a file somebody has edited. An empty file would work just as well, since every key falls back on its own; it would also teach nothing, and the spelling of the keys is the part nobody can guess.

A copy rather than a template of its own, so `init` and `show default` cannot drift: the file `init` writes is the file the built-in is documented by, `internal/wisp/bundles/default/workflow.yaml` in the source.

```
$ wisp workflow init calm
~/.config/wisp/workflows/calm/workflow.yaml

it is a copy of the built-in, so it changes nothing until you edit it:
  wisp workflow edit calm
  wisp workflow use calm
```

`--here` puts it in `<workspace>/.wisp/workflows/` instead, and says at that moment that the workspace's own workflow has to be accepted before it runs, rather than leaving that to surface as a note on the next session.

```
$ wisp workflow init ship --here
/Users/you/work/.wisp/workflows/ship/workflow.yaml

it is a copy of the built-in, so it changes nothing until you edit it:
  wisp workflow edit ./ship
  wisp workflow use ./ship

this one is the workspace's, so it also has to be accepted before it runs:
  wisp workflow accept ./ship
```

```
usage: wisp workflow init <name> [--from <shipped>] [--here]

the name is a directory: `wisp workflow init solo` makes solo yours;
--from starts from one of the bundles wisp ships (default)
wisp does not ship a workflow called "x"

the ones it does: default
"a/b" is not a workflow name: one directory segment, no slashes
~/.config/wisp/workflows/hooked already exists

edit it:
  wisp workflow edit hooked

or pick another name
cannot locate a config directory to put it in
could not create <path>: <err>
```

### `wisp workflow use <name> [--here]`

Write the `workflow:` key, through a YAML node so the comments in the file survive. The two destinations answer different questions: the user config is "this is how I work", inherited by every workspace that does not say otherwise, and `--here` is "this is how work happens here", travelling with the repos to everyone who checks them out.

```
$ wisp workflow use ./ship --here
workflow: ./ship
  in /Users/you/work/.wisp.yaml

nothing of it runs until it has been read and accepted:
  wisp workflow accept ./ship

  wisp workflow   what that changed, key by key
```

```
usage: wisp workflow use <name> [--here]

`wisp workflow list` is what there is to bind to
no workflow "nope" at ~/.config/wisp/workflows/nope

make it first:
  wisp workflow init nope

or see what there is:
  wisp workflow list
cannot locate a config directory to record this in
```

### `wisp workflow edit [<name>]`

Open a bundle's `workflow.yaml` in `$EDITOR`, then `$VISUAL`, then `vi`, and re-read it afterwards. The re-read is the whole reason to go through wisp rather than the editor alone: a manifest that no longer parses costs its keys silently, and the moment to be told is while you still remember what you changed. With no name it edits whatever is bound here.

A file that no longer parses is an error. A file that parses but holds a key wisp does not know is a note on **stderr**, printed before the success line, because an unknown key parses fine and then quietly does nothing:

```
$ wisp workflow edit typo
wisp: ~/.config/wisp/workflows/typo/workflow.yaml: yaml: unmarshal errors:
  line 3: field progam not found in type wisp.Workflow
~/.config/wisp/workflows/typo/workflow.yaml

  wisp workflow show typo   what it sets now
```

`EDITOR` is split on spaces rather than handed to a shell, so `code -w` works.

```
$ wisp workflow edit default
wisp: that is the built-in workflow, which is compiled into wisp

make your own copy of it and edit that:
  wisp workflow init solo
  wisp workflow use solo
  wisp workflow edit solo
```

```
EDITOR is set to whitespace, so there is nothing to run
<editor>: <err>

set EDITOR to something that is installed, or edit <path> directly
<the parse error>

wisp falls back to the built-in for every key it cannot read there, so this
is worth fixing now:
  wisp workflow edit <name>
```

### `wisp workflow accept [-y]`, `accept ./<name>`, `accept .wisp.yaml`, `accept <item>`

Read something that arrived with a repo, and record that it may run. This is the only security decision wisp has, so it prints the file in full, then the body of every script that file names, then asks. A hook naming a file that is not there yet prints as `not there yet` rather than being skipped, because that is still a file the workspace decides the contents of later. Scripts are never truncated.

**With no argument it is the whole workspace**: every config file and every hook script wisp would run here, printed in full, counted, and authorised in one answer. It exists because a hook script inside the workspace is gated whoever named it, and the built-in's `provision:` default is named by no file at all, so there is no address anybody could type for it. Asked of a workspace that runs nothing, it says so and writes nothing. It stops at the workspace: items are accepted one at a time, since a vault holds hundreds of them and almost none set an executable key.

| address | what it covers | recorded as |
|---|---|---|
| *(none)* | everything above, in one answer | one record per thing |
| `./<name>` | a bundle under `.wisp/workflows/`, and the scripts it names | SHA-256 of the whole directory, plus one per script |
| `.wisp.yaml` | the workspace config's own `program:`, hooks and `layout[].run` | SHA-256 of the file, plus one per script it names |
| `<item>` | an item's `orchestration.md`, same keys | SHA-256 of the file, plus one per script it names |

A hook script is recorded separately from the file that named it, under `script <path>`, because the file and the script are different bytes and only one of them is what runs. Accepting a file used to record the file alone, so a later commit could rewrite a script it named and the acceptance held.

The gate is on **executable keys only**: `program`, `source`, `context`, `close`, `provision`, and any `layout` entry with a `run:` in it, plus the contents of any hook script that lands inside the workspace. `branch:`, `worktree:` and `status.needs_input` are strings wisp interprets itself and apply from any file with no ceremony. A file setting none of the executable keys needs no acceptance and says so:

```
.wisp.yaml runs nothing, so there is nothing to accept

it sets no program, no hooks and no layout command, and every other key in it
already applies.
```

Until accepted, those keys are **stripped and the session still opens**, on the built-in's values, with a note saying what was dropped and how to allow it:

```
note: .wisp.yaml sets program, layout, which wisp would run; it has not been accepted, so run
`wisp workflow accept .wisp.yaml` after reading it
```

A hook script that is inside the workspace and not accepted is dropped the same way, and the note names the command that takes no argument:

```
note: provision names .claude/scripts/provision-worktree.sh, a script this workspace supplies that
has not been accepted; run `wisp workflow accept` after reading it
```

`-y` (or `--yes`) answers yes without the prompt, and is **required rather than assumed** when stdin is not a terminal, because a prompt written to something that cannot answer is either a hang or a silent yes.

The record goes into the user config under `accepted:`, keyed by workspace path and address together. Never into the workspace config: a workspace that could write it would be accepting itself. For a bundle the hash covers every file in the directory, not just the manifest, which is what makes showing you the scripts worth anything: a `git pull` rewriting `bin/close.sh` alone puts it back to unaccepted.

```
accepted ./ship in work, recorded in ~/.config/wisp/config.yaml

editing anything in it, the workflow.yaml or a script it names, puts it back to
unaccepted, which is the point: this is not a standing permission for whatever
the bundle becomes later.
```

```
no workflow "nope" anywhere: not one of yours under ~/.config/wisp/workflows, and not one this workspace ships

  wisp workflow list   what there is to name
no item "_adhoc/nope" in ~/work/working_items

  wisp ls   what there is to name
only a workflow this workspace supplies has to be accepted, and those are addressed ./hooked

"hooked" is one of yours, under ~/.config/wisp/workflows, and yours already run
"ship" names one of yours; this workspace ships one by that name too

the workspace's one is the one that needs accepting:
  wisp workflow accept ./ship
this needs an answer and there is no terminal to ask on

read the above and say so outright:
  wisp workflow accept ./ship -y
not accepted, and nothing was written

run it again once you have read it:
  wisp workflow accept ./ship
<the parse error>

wisp cannot tell you what this would run, so it will not record that you
agreed to it. Fix the file, or ask whoever ships it to
```

`./ship is already accepted, exactly as it stands now` is printed, and nothing written, when the hash already matches.

### `wisp workflow push <name> <host>`

Copy one of your own bundles to another machine. A workflow does not cross a host boundary on its own, so across machines it is necessarily a copy; this is wisp making one rather than leaving it to `scp`. The directory is packed with `tar` and sent down the ssh connection wisp is already opening, and the destination is expanded by the **far side's** shell as `"${XDG_CONFIG_HOME:-$HOME/.config}"/wisp/workflows`.

```
$ wisp workflow push quiet localhost
quiet to localhost:~/.config/wisp/workflows/quiet  (1 files, 75 B)

check it landed:
  wisp workflow list --host localhost
```

The `~/.config/wisp/workflows/` in that line is how it reads, not what was sent: the destination is expanded by the far side's shell, so a machine with `XDG_CONFIG_HOME` set puts it somewhere else.

The file count is of the whole tree, matching what `tar` ships, so a bundle keeping its scripts in `bin/` reports all of them. `push` is idempotent, so re-running it is how you resync. It refuses a `./` address, since a workspace's own workflow travels with the workspace.

```
usage: wisp workflow push <name> <host>

`wisp host` is the machines wisp can reach
./ship belongs to this workspace, so it travels with it; there is nothing to push
no workflow "nope" at ~/.config/wisp/workflows/nope
<dir> has no workflow.yaml, so there is nothing to push
packing <dir>: <tar's last line>
```

### `wisp workflow help`, `-h`, `--help`

The workflow usage block, to stdout, exit 0. The same block is appended to a bad flag and to an unknown item name.

---

## Machine-facing

These are ordinary commands, not a mode. The far side is just wisp, answering about the workspace it owns. You can run them by hand; they are documented because the output shapes are stable and scriptable.

### `wisp board [--gitlab]`

This workspace's state as one line of JSON. `--gitlab` folds in the GitLab source as well. `--json` is accepted and ignored: this command always emits JSON.

```json
{"wire":1,"wisp":"0.17.0","ready":true,"items":[{"name":"repo/318-slug","state":2,"title":"…","done":true}],"live":3,"attn":1}
```

`state` is the raw ladder: 0 gitlab, 1 folder, 2 live, 3 needs input. `title` and `done` are omitted when empty. `attn` counts toward `live` as well.

A workspace that does not exist is reported as `"ready":false` and **exit 0**, not raised. "The host answered and there is no vault there" and "the host did not answer" are different problems with different fixes, and collapsing them into one non-zero exit would throw that away. For the same reason a non-fatal problem, such as an unconfigured GitLab source, comes back as a `note` field rather than an error.

### `wisp ws --json`

What this machine holds, for another machine that has it registered.

```json
{"wire":1,"wisp":"0.17.0","default":"work","workspaces":[{"name":"work","path":"/Users/you/work","ready":true,"live":2,"attn":1}]}
```

Its **own local** workspaces only. A machine that enumerated its remote ones would let two of them holding each other enumerate forever.

### `wisp preview <item> [--width n]`

The picker's preview pane as plain text, no trailing newline. `--width` defaults to 80 and a non-numeric value falls back to it silently. It never returns an error: a remote failure is rendered as the body, because the pane is a place to show text and an empty pane says nothing.

---

## Meta

### `wisp version`, `wisp --version`, `wisp -v`

Prints `wisp 0.17.0`. Recognised only as the first argument.

### `wisp help`, `wisp --help`, `wisp -h`

The usage text, to stdout, exit 0. Recognised only as the first argument. An unknown command prints the same text to **stderr** and exits 1 — and that string is load-bearing across the network: a far side too old to know a command prints `unknown command`, which is what lets this end say `wisp on eldo is too old for this` instead of `exit status 255`.

---

## Streams and exit codes

| | |
|---|---|
| **stdout** | `version`, `help`, `ls`, `ws`, `host`, `repos`, `board`, `preview`, `new`, `done`, every form of `workflow`, and the confirmations from `kill`, `ws new` and `host add`. `provision`'s progress lines. |
| **stderr** | Every error. `open`'s progress lines, including the workflow notes it logs. `provision`'s failure block. The provisioning script's own output, both streams. |
| **exit 0** | Success, `help`, `version`, and `provision` even when it failed. |
| **exit 1** | Everything else. |

Stable enough to parse: `board`, `ws --json` and `new --json` (single-line JSON with a trailing newline), `repos` (one per line), `preview` (raw text). `ls`, `ws`, `host` and `workflow` use fixed-width columns; `ls`, `ws` and `host` overflow rather than truncate, and `workflow`'s value column truncates from the left at 44 characters. Readable, but not a format to depend on.

---

## Known rough edges

Small parsing gaps, listed so they are not mistaken for behaviour worth relying on.

- `wisp -w side version` and `wisp -w side help` fail with `unknown command`.
- `wisp new --json` with no name creates `_adhoc/json`.
- `wisp preview --width 80` with no item previews an item named `--width`.
- `wisp ws new --json` creates a workspace named `--json`.
- `wisp repos` with no repos prints a blank line rather than nothing.
- `open`'s progress lines go to stderr from the command line and stdout from the picker.
