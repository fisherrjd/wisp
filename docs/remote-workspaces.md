# Remote workspaces

Status: **built** as of v0.11.0, with the exceptions at the bottom. Workspaces and the hop ring shipped first on purpose: a remote workspace is a workspace with a host, and everything below plugs into machinery that already existed.

## The decision everything follows from

**The picker always runs locally. Only the agent session runs remotely.**

The alternative, running the picker on the far side over `ssh -t`, makes every keystroke a network round trip for the sake of redrawing a list. The picker is already built to load slowly and paint fast: local candidates arrive first and the remote source folds in when it answers. A machine across the network is just another slow source.

## Config

A workspace's location grows a host. Both spellings are accepted, because the scp shorthand is already in everyone's fingers and the explicit form is what you want once there is more to say.

```yaml
workspaces:
  work: ~/work                    # local, unchanged
  desktop: bigbox:~/work          # shorthand
  laptop:                         # explicit
    host: macbook
    path: ~/work
```

`Workspaces map[string]string` becomes `map[string]Location` where `Location` is `{Host, Path}` and an empty host means local. A custom `UnmarshalYAML` accepts a scalar or a mapping.

A scalar is remote when it holds a colon before the first slash. That makes `/var/lib/a:b` a local path and `bigbox:~/work` a remote one, with no mode flag to set and no ambiguity worth worrying about.

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

`board` reports one workspace and consults no other. It is the one place where that has to be said out loud: the obvious implementation reuses the picker's own loader, which builds the workspace tally, which probes every remote workspace the machine has configured. A board request that did that would walk from host to host with nothing to stop it, and a pair of machines each holding the other would never return at all.

The same reasoning is why the tally does not add wrapper sessions to a remote workspace's counts. The far side already knows about every session it owns, including the ones this machine is attached to, so counting the wrapper as well would report each of them twice.

## Settled

**`ctrl-x` on a remote item kills the remote session**, not just the wrapper. It means the same thing everywhere: stop this work. Walking away and leaving it running is what `esc` already does, so kill has to mean the heavier thing or there is no way to say it.

**Killing races, and that is fine.** Ending the far session ends the ssh, which closes the window, which takes the wrapper with it, usually before the local kill runs. The local failure only counts if the session is still standing afterwards.

## Not done

**The preview is not debounced.** Every cursor move on a remote workspace is an ssh round trip. The shared connection makes it about 30ms and the loader is asynchronous, so it is not felt on a good link; on a bad one it will be. The fix is a rest timer before the fetch, not a change to anything above.

**Nested tmux is documented, not handled.** Two servers are stacked when you attach to a remote item and the prefix key means two things. The wrapper's status bar says which workspace and host you are in; deciding what the prefix does belongs in your own tmux config, either a different prefix on the remote or a key bound to `send-prefix`.

**`wisp ws new` against a host only registers it.** The directory, the vault and the `.wisp.yaml` are the far side's, and reaching across to create them would be one machine deciding how another is laid out. Run `wisp ws new` over there.
