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
| `some name` | `_adhoc/some-name` |

Only the first `/` splits, so the result is always exactly two levels. An item that already exists is handed back rather than refused, which is what makes `ctrl-n` on an existing name just open it.

A pasted link **does not have to be assigned to you**. Being the assignee is what fills the `+` section of the picker; it has never had anything to do with whether you can open something.

`--json` prints `{"name":"_adhoc/thing"}` instead of the bare name. That form is how one wisp asks another to create an item on the machine that owns the workspace.

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

```
usage: wisp kill <item>
no session for <item>
could not kill <session>: <err>
```

### `wisp provision <item>`

Build any worktrees the item's manifest declares and rewrite its context file. wisp runs this itself, in a side window, whenever a session opens with worktrees still missing; it is also useful by hand after deleting one.

It **exits 0 even when it fails**, printing the error and waiting for a keypress instead. That is deliberate: it runs in a tmux window that closes the moment its command returns, and a failure that vanishes is one nobody can read.

```
usage: wisp provision <item>
provisioning script missing: <workspace>/.claude/scripts/provision-worktree.sh
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

### `wisp ws new [-p|--parents] <name> [path]`

Make a directory a workspace and register it. The path defaults to `.`, and may be local or `host:path`. `-p` creates the directory too, and is accepted in any position: a trailing `-p` should not become a directory named `-p`.

It creates the vault and a fully commented `.wisp.yaml` to fill in, then registers the name, adding `default:` if nothing already answers that question. Run on a directory that is already a workspace, it just registers it, which is how you make an existing vault reachable by `hop`.

Without `-p` the directory has to exist. A mistyped path should fail there and then rather than become a workspace somewhere nobody meant to put one, where the mistake only surfaces later as a picker with nothing in it.

```
usage: wisp ws new [-p] <name> [path]
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

## Machine-facing

These are ordinary commands, not a mode. The far side is just wisp, answering about the workspace it owns. You can run them by hand; they are documented because the output shapes are stable and scriptable.

### `wisp board [--gitlab]`

This workspace's state as one line of JSON. `--gitlab` folds in the GitLab source as well. `--json` is accepted and ignored: this command always emits JSON.

```json
{"wire":1,"wisp":"0.16.0","ready":true,"items":[{"name":"repo/318-slug","state":2,"title":"…","done":true}],"live":3,"attn":1}
```

`state` is the raw ladder: 0 gitlab, 1 folder, 2 live, 3 needs input. `title` and `done` are omitted when empty. `attn` counts toward `live` as well.

A workspace that does not exist is reported as `"ready":false` and **exit 0**, not raised. "The host answered and there is no vault there" and "the host did not answer" are different problems with different fixes, and collapsing them into one non-zero exit would throw that away. For the same reason a non-fatal problem, such as an unconfigured GitLab source, comes back as a `note` field rather than an error.

### `wisp ws --json`

What this machine holds, for another machine that has it registered.

```json
{"wire":1,"wisp":"0.16.0","default":"work","workspaces":[{"name":"work","path":"/Users/you/work","ready":true,"live":2,"attn":1}]}
```

Its **own local** workspaces only. A machine that enumerated its remote ones would let two of them holding each other enumerate forever.

### `wisp preview <item> [--width n]`

The picker's preview pane as plain text, no trailing newline. `--width` defaults to 80 and a non-numeric value falls back to it silently. It never returns an error: a remote failure is rendered as the body, because the pane is a place to show text and an empty pane says nothing.

---

## Meta

### `wisp version`, `wisp --version`, `wisp -v`

Prints `wisp 0.16.0`. Recognised only as the first argument.

### `wisp help`, `wisp --help`, `wisp -h`

The usage text, to stdout, exit 0. Recognised only as the first argument. An unknown command prints the same text to **stderr** and exits 1 — and that string is load-bearing across the network: a far side too old to know a command prints `unknown command`, which is what lets this end say `wisp on eldo is too old for this` instead of `exit status 255`.

---

## Streams and exit codes

| | |
|---|---|
| **stdout** | `version`, `help`, `ls`, `ws`, `host`, `repos`, `board`, `preview`, `new`, `done`, and the confirmations from `kill`, `ws new` and `host add`. `provision`'s progress lines. |
| **stderr** | Every error. `open`'s progress lines. `provision`'s failure block. The provisioning script's own output, both streams. |
| **exit 0** | Success, `help`, `version`, and `provision` even when it failed. |
| **exit 1** | Everything else. |

Stable enough to parse: `board`, `ws --json` and `new --json` (single-line JSON with a trailing newline), `repos` (one per line), `preview` (raw text). `ls`, `ws` and `host` use fixed-width columns that overflow rather than truncate — readable, but not a format to depend on.

---

## Known rough edges

Small parsing gaps, listed so they are not mistaken for behaviour worth relying on.

- `wisp -w side version` and `wisp -w side help` fail with `unknown command`.
- `wisp new --json` with no name creates `_adhoc/json`.
- `wisp preview --width 80` with no item previews an item named `--width`.
- `wisp ws new --json` creates a workspace named `--json`.
- `wisp repos` with no repos prints a blank line rather than nothing.
- `open`'s progress lines go to stderr from the command line and stdout from the picker.
