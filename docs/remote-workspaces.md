# Remote workspaces

Status: **built** as of v0.11.0, with the exceptions at the bottom. Workspaces and the hop ring shipped first on purpose: a remote workspace is a workspace with a host, and everything below plugs into machinery that already existed.

## The decision everything follows from

**The picker always runs locally. Only the agent session runs remotely.**

The alternative, running the picker on the far side over `ssh -t`, makes every keystroke a network round trip for the sake of redrawing a list. The picker is already built to load slowly and paint fast: local candidates arrive first and the remote source folds in when it answers. A machine across the network is just another slow source.

## Config: a machine, not a path

```yaml
hosts:
  - jade@eldo
  - bigbox
```

The unit is the machine, because the machine already knows what it holds. Registering its workspaces here as well would be a second copy of a list only one side owns, and the two would drift the moment one was made over there. It is also cheaper: one round trip per machine, where a per-workspace list costs one each and gets slower the more you add.

Names default to the hostname, without the account in front or the domain behind. A machine's **default workspace takes its bare name**, and anything else it holds is `eldo/side`: one machine usually holds one workspace, and `eldo/work` where `eldo` would do is ceremony. The mapping form, `box: jade@eldo`, renames a machine when the hostname is not what you want to call it.

A machine's config is a list of what it was told about, not a scan of its disk, so a workspace it has never registered still needs a path here:

```yaml
workspaces:
  work: ~/work                    # local
  scratch: bigbox:~/scratch       # a path on a machine
```

`Location` is `{Host, Path, Name}`. An empty host is local. A discovered workspace carries a host and a `Name` but no path, and is asked for by that name (`wisp -w side board --json`), because the machine that owns it is the one that knows where it is. A path copied over here would be a second answer to a question only one side can answer, and it would also mean resolving a name required a network call — this way it is pure syntax.

A scalar location is remote when it holds a colon before the first slash. That makes `/var/lib/a:b` a local path and `bigbox:~/work` a remote one, with no mode flag to set. A trailing colon with nothing after it is the machine itself, which is what lets one create line in the picker add either.

## The wire

wisp on the far side is the authority. Local wisp never touches the remote filesystem or the remote tmux server directly: driving raw `tmux ls` and `capture-pane` over ssh would be a round trip per session per redraw, and it would put a second, subtly different implementation of the workspace model on the near side.

```
ssh <host> wisp -w work board --json     items, states, tally, ready
ssh <host> wisp -w work preview <item>   the pane text for the preview
ssh -t <host> wisp -w work open <item>   creates and attaches, interactively
```

`board --json` carries a `wire` integer alongside the version. A mismatch refuses, naming both versions, rather than half-parsing a shape it does not understand. The two ends are separate installs and will drift.

Every non-interactive call is made with:

| | |
|---|---|
| `BatchMode=yes` | A host that wants a password fails immediately instead of hanging the picker on a prompt nobody can see. |
| `ConnectTimeout=3` | A dead host is a row that does not appear, not a picker that never opens. |
| `ControlMaster=auto`, `ControlPersist=60s` | The first call pays for the handshake; the rest are local-ish. Without it a preview per cursor move is unusable. |

**wisp does no authentication.** If `ssh bigbox` works in your shell it works here, and if it does not, that is an ssh config problem with an ssh config fix. No keys, no passwords, no agent handling, nothing to leak.

## Attaching: the local wrapper

Opening a remote item creates a **local** tmux session, `wisp_desktop_<item>`, whose one window runs:

```
ssh -t bigbox wisp -w work open <item>
```

The remote wisp builds the real session there and attaches to it. One decision, and almost nothing else has to change:

- `Cycle` walks the wrapper like any other session.
- `claim` already filters on `@wisp_ws`, so the wrapper lands in the right workspace.
- The last-visited session, `hop`, and `Remember` are local tmux server options and never learn that anything is remote.
- `NeedsInput` captures the wrapper's pane, which is rendering the remote pane, so the `?` state works with no extra machinery for anything you are attached to.

Sessions running remotely that you are not attached to have no wrapper and are invisible locally. Those come from `board --json`, and the two lists merge on `Item.Key()` exactly as the local and GitLab sources already do.

The cost is nested tmux and a prefix key that now means two things. The fixes are the usual ones, a different prefix on the remote or a key bound to `send-prefix`, and they belong in your tmux config rather than in wisp rewriting it. wisp marks the wrapper session's status bar so it is obvious you are a level down.

## Failure is a state, not an exception

An unreachable host must never empty the list you are looking at. This is the same rule the GitLab source already follows, for the same reason: a missing optional source and a source with nothing in it look identical, and the silent version costs a debugging session.

`Peer.Ready` generalises to cover it. A remote workspace that will not answer is marked in the picker's header and `hop next` steps over it, exactly as a local workspace whose directory does not exist. Named outright it still fails loudly, because you asked for that one.

Three failures get their own message rather than a raw shell error:

1. host unreachable
2. `wisp` not on the far side's PATH
3. wire version mismatch

## What a board must not do

`board` reports one workspace and consults no other, and `ws --json` reports only the machine's own workspaces. It is the one thing that has to be said out loud twice: the obvious implementation of each reuses the picker's own loader, which builds the workspace tally, which asks every machine configured here. A pair of machines each holding the other would never return at all.

`ws --json` is narrower still. It reports what the machine has **registered**, not the workspace its current directory happens to resolve to — an ssh command lands in `$HOME`, and reporting whatever that resolved to would put a row in the asking machine's ring for a workspace nobody registered anywhere.

The same reasoning is why the tally does not add wrapper sessions to a remote workspace's counts. The far side already knows about every session it owns, including the ones this machine is attached to, so counting the wrapper as well would report each of them twice.

## Settled

**`ctrl-x` on a remote item kills the remote session**, not just the wrapper. It means the same thing everywhere: stop this work. Walking away and leaving it running is what `esc` already does, so kill has to mean the heavier thing or there is no way to say it.

**Killing races, and that is fine.** Ending the far session ends the ssh, which closes the window, which takes the wrapper with it, usually before the local kill runs. The local failure only counts if the session is still standing afterwards.

## Not done

**The preview is not debounced.** Every cursor move on a remote workspace is an ssh round trip. The shared connection makes it about 30ms and the loader is asynchronous, so it is not felt on a good link; on a bad one it will be. The fix is a rest timer before the fetch, not a change to anything above.

**Nested tmux is documented, not handled.** Two servers are stacked when you attach to a remote item and the prefix key means two things. The wrapper's status bar says which workspace and host you are in; deciding what the prefix does belongs in your own tmux config, either a different prefix on the remote or a key bound to `send-prefix`.

**Nothing needs a config file edited by hand.** `wisp ws new <name> host:path` registers a remote workspace, and `-p` also makes it over there, by running the same command on the far side rather than reaching into its filesystem. Both work from the picker's own create line, so adding a machine never means leaving the TUI.

The ssh user goes in the location, `jade@eldo:~/work`, so reaching a host whose account does not match the local one needs no `~/.ssh/config` entry either.
