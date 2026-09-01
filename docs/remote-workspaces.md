# Remote workspaces

A workspace on another machine, in the same ring as the ones on this one. Wire version 1, as of wisp 0.17.0.

Workspaces and the hop ring shipped first on purpose: a remote workspace is a workspace with a host, and everything below plugs into machinery that already existed.

## The decision everything follows from

**The picker always runs locally. Only the agent session runs remotely.**

The alternative, running the picker on the far side over `ssh -t`, makes every keystroke a network round trip for the sake of redrawing a list. The picker is already built to load slowly and paint fast: local candidates arrive first and the remote source folds in when it answers. A machine across the network is just another slow source.

## Config: a machine, not a path

```yaml
hosts:
  - jade@eldo
  - bigbox

hosts:                            # or a mapping, to call one something else
  box: jade@eldo
```

The unit is the machine, because the machine already knows what it holds. Registering its workspaces here as well would be a second copy of a list only one side owns, and the two would drift the moment one was made over there. It is also cheaper: one round trip per machine, where a per-workspace list costs one each and gets slower the more you add.

Names default to the hostname, without the account in front or the domain behind, so `jade@eldo.local` is `eldo`. A machine's **default workspace takes its bare name**, and anything else it holds is `eldo/side`: one machine usually holds one workspace, and `eldo/work` where `eldo` would do is ceremony. Which one is the default is the far side's answer, not a guess made here.

A machine's config is a list of what it was told about, not a scan of its disk, so a workspace it has never registered still needs a path here:

```yaml
workspaces:
  work: ~/work                    # local
  scratch: bigbox:~/scratch       # a path on a machine
```

A location is `{Host, Path, Name}`. An empty host is local. A **discovered** workspace carries a host and a `Name` but no path, and is asked for by that name, because the machine that owns it is the one that knows where it is. A path copied over here would be a second answer to a question only one side can answer, and it would also mean resolving a name required a network call — this way it is pure syntax.

A scalar location is remote when it holds a colon before the first slash. That makes `/var/lib/a:b` a local path and `bigbox:~/work` a remote one, with no mode flag to set. A trailing colon with nothing after it is the machine itself, which `wisp ws new` refuses by name:

```
bigbox is a machine, not a workspace on one

  wisp host add <name> bigbox

gets you every workspace it holds
```

## The wire

wisp on the far side is the authority. Local wisp never touches the remote filesystem or the remote tmux server directly: driving raw `tmux ls` and `capture-pane` over ssh would be a round trip per session per redraw, and it would put a second, subtly different implementation of the workspace model on the near side.

Every call is one of the two addressing forms. A discovered workspace is named; one written down with a path carries the path in the environment, because the far side may never have registered it at all.

```
ssh <host> wisp ws --json                          which workspaces the machine holds
ssh <host> wisp -w work board --json               items, states, tally, ready
ssh <host> wisp -w work board --json --gitlab      the same, with its GitLab source folded in
ssh <host> wisp -w work preview <item> --width 80  the pane text for the preview
ssh <host> wisp -w work new <input> --json         makes an item over there, returns the name
ssh <host> wisp -w work kill <item>                stops one
ssh <host> wisp -w work repos                      its repo checkouts
ssh <host> wisp ws new <name> -p <path>            makes a workspace over there
ssh -t <host> wisp -w work open <item>             creates and attaches, interactively

ssh <host> WISP_WORKSPACE=~/scratch wisp board --json    a workspace written down by path
```

The tilde in that last form is deliberately left outside the quoting so the far side's shell expands it. Quoting the whole path would hand wisp a literal `~`, which is not a directory anywhere.

### Versioning

Both `board --json` and `ws --json` carry a `wire` integer alongside the version, and both ends check it. A mismatch refuses by name and number rather than half-parsing a shape it does not understand:

```
eldo runs wisp 0.14.0 speaking wire 0; this one speaks wire 1
eldo: unreadable board; wisp there is probably older than this one (0.17.0)
```

Fields are added without bumping the number when they can be. `ItemJSON.done` is omitted when false, so an older wisp on either end ignores a field it does not know and reads a missing one as not done, which is the behaviour it had before the flag existed.

`BoardJSON` also carries a `note`, for something worth saying that is not a failure — an unconfigured GitLab source, most often. Reported rather than swallowed, for the same reason it is locally: an off source and an empty one look identical in the picker.

### ssh options

Every call, interactive or not, carries the multiplexing block:

| | |
|---|---|
| `ControlMaster=auto` | |
| `ControlPath=~/.ssh/wisp-%C` | The first call pays for the handshake and the rest reuse it. Without this a preview per cursor move is unusable. `%C` is a hash rather than the hostname, which also keeps the socket path inside the length a unix socket allows. |
| `ControlPersist=60s` | |

Background calls add two more:

| | |
|---|---|
| `BatchMode=yes` | A host that wants a password fails immediately instead of hanging the picker on a prompt nobody can see. It forbids password auth, key passphrases and host-key confirmation alike. |
| `ConnectTimeout=3` | A dead host is a row that does not appear, not a picker that never opens. |

Interactive calls — only `open` — add `-t` instead, and take neither of those. That one *can* prompt, because there is a terminal in front of it.

Note that wisp creates control sockets in your `~/.ssh`.

**wisp does no authentication.** If `ssh bigbox` works in your shell it works here, and if it does not, that is an ssh config problem with an ssh config fix. No keys, no passwords, no agent handling, nothing to leak.

## Attaching: the local wrapper

Opening a remote item creates a **local** tmux session, `wisp_<workspace>_<item>`, whose one window runs:

```
ssh -t bigbox wisp -w work open <item> || { echo; echo 'connection to bigbox ended; press enter'; read -r _; }
```

The remote wisp builds the real session there and attaches to it. The trailing clause keeps a failure on screen: tmux closes a window the moment its command returns, so an ssh that cannot connect would take the session with it and look exactly like wisp creating nothing at all.

One decision, and almost nothing else has to change:

- `Cycle` walks the wrapper like any other session.
- `claim` already filters on `@wisp_ws`, so the wrapper lands in the right workspace.
- The last-visited session, `hop`, and `Remember` are local tmux server options and never learn that anything is remote.
- `NeedsInput` captures the wrapper's pane, which is rendering the remote pane, so the `?` state works with no extra machinery for anything you are attached to.

Sessions running remotely that you are not attached to have no wrapper and are invisible locally. Those come from `board --json`, and the two lists merge on identity exactly as the local and GitLab sources already do.

The cost is nested tmux and a prefix key that now means two things. The fixes are the usual ones, a different prefix on the remote or a key bound to `send-prefix`, and they belong in your tmux config rather than in wisp rewriting it. The wrapper's status bar is turned on and set to `<workspace>:<host>`, so being a level down is something you see rather than something you discover.

## Failure is a state, not an exception

An unreachable host must never empty the list you are looking at. This is the same rule the GitLab source already follows, for the same reason: a missing optional source and a source with nothing in it look identical, and the silent version costs a debugging session.

`Peer.Ready` covers being unusable and `Peer.Unreachable` separates the two ways of it: both stop you working there, but one is fixed by making a directory and the other by fixing ssh, and one glyph for both would send you after the wrong one. Either way `hop next` steps over it. Named outright it still fails loudly, because you asked for that one.

The raw failure is `exit status 255`, which is true and useless. Six cases get their own message instead:

| when | you get |
|---|---|
| the far side prints `unknown command` | `wisp on eldo is too old for this (this one is 0.17.0)` |
| exit 127 | `wisp is not on PATH on eldo` |
| exit 255 | `cannot reach eldo: ssh: connect to host eldo port 22: Operation timed out` |
| the JSON does not parse | `eldo: unreadable board; wisp there is probably older than this one (0.17.0)` |
| the wire number differs | `eldo runs wisp 0.14.0 speaking wire 0; this one speaks wire 1` |
| anything else non-zero | `eldo: <the last line of stderr>` |

The too-old check runs first and is a substring test on stderr, because a far side that rejects the command prints its own usage, whose last line says nothing about why.

Two more come from the tally rather than from ssh: `no vault at <path> on <host>`, and `no workspaces registered on <host>; make one there, or add it here with a path`.

## What a board must not do

`board` reports one workspace and consults no other, and `ws --json` reports only the machine's own workspaces. It is the one thing that has to be said out loud twice: the obvious implementation of each reuses the picker's own loader, which builds the workspace tally, which asks every machine configured here. A pair of machines each holding the other would never return at all.

`ws --json` is narrower still. It reports what the machine has **registered**, not the workspace its current directory happens to resolve to — an ssh command lands in `$HOME`, and reporting whatever that resolved to would put a row in the asking machine's ring for a workspace nobody registered anywhere.

The same reasoning is why the tally does not add wrapper sessions to a remote workspace's counts. The far side already knows about every session it owns, including the ones this machine is attached to, so counting the wrapper as well would report each of them twice.

## Two rounds, overlapped

Loading the picker asks two independent questions: which workspaces each machine holds, and what each written-down remote workspace contains. Each round is internally parallel, one goroutine per target, and the two rounds run **at the same time as each other**.

They used to run in sequence, which made the picker wait the sum rather than the slower of the two. A machine that is asleep costs the full connect timeout once per round, so that difference is three seconds per sleeping desktop on every load, every `wisp ws`, and every ring hop.

The board probe for the workspace you are actually in is reused for both the list and the header, because there they answer the same question.

## Setting one up

Nothing needs a config file edited by hand.

```
wisp host add jade@eldo                  # the machine, and everything on it
wisp host add box jade@eldo              # calling it something else
wisp ws new scratch bigbox:~/scratch     # one workspace it has not registered
wisp ws new scratch -p bigbox:~/scratch  # and make it there too
```

`-p` runs the same command on the far side rather than reaching into its filesystem. Both have keys in the picker's tree: `a` for a machine, `n` for a workspace.

They are two commands rather than one because they are two things at two levels. An earlier version told them apart by a trailing colon on a shared line, which is the tree flattened into syntax and not something anyone would guess.

The ssh user goes in the target, `jade@eldo`, so reaching a machine whose account does not match the local one needs no `~/.ssh/config` entry either.

A machine that does not answer is still added. It is almost always a typo, but it is also sometimes a desktop that is asleep, and forgetting one is a single keystroke.

### What the far side needs

| | if it is missing |
|---|---|
| **wisp on `PATH` in a non-interactive ssh** | `wisp is not on PATH on <host>`. This is the common one: `ssh host cmd` runs a non-login, non-interactive shell, so `~/.bash_profile` and interactive-only blocks in `~/.zshrc` are skipped. |
| **key-based auth** | `cannot reach <host>: Permission denied (publickey,password).` Background calls set `BatchMode`, so there is nowhere to type a password. |
| **the host key already known** | `cannot reach <host>: Host key verification failed.` |
| **a wisp new enough to know these commands** | `wisp on <host> is too old for this` |
| **the same wire number** | the mismatch message above |
| **a vault that exists** | not an error: the row shows `no vault at <path> on <host>`. wisp never creates one. |
| **at least one registered workspace** | `no workspaces registered on <host>; make one there, or add it here with a path` |

## Settled

**`ctrl-x` on a remote item kills the remote session**, not just the wrapper. It means the same thing everywhere: stop this work. Walking away and leaving it running is what `esc` already does, so kill has to mean the heavier thing or there is no way to say it.

**Killing races, and that is fine.** Ending the far session ends the ssh, which closes the window, which takes the wrapper with it, usually before the local kill runs. The local failure only counts if the session is still standing afterwards.

## Not done

**The preview is not debounced.** Every cursor move on a remote workspace is an ssh round trip. The shared connection makes it about 30ms and the loader is asynchronous, so it is not felt on a good link; on a bad one it will be. The fix is a rest timer before the fetch, not a change to anything above.

**`wisp done` is local only.** Every other item command has a remote branch; this one writes to a local path and fails against a remote workspace. Closing out an item on another machine means doing it there, or with `ctrl-d` in the picker, which acts on the vault that owns the item.

**Nested tmux is documented, not handled.** Two servers are stacked when you attach to a remote item and the prefix key means two things. The wrapper's status bar says which workspace and host you are in; deciding what the prefix does belongs in your own tmux config, either a different prefix on the remote or a key bound to `send-prefix`.
