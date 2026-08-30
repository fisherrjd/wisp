# Remote workspaces

Status: **plan**. Nothing here is built yet. Workspaces and the hop ring (v0.9.0) are the local half of it and shipped first on purpose: a remote workspace is a workspace with a host, and everything below plugs into machinery that already exists.

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

## Phases

1. **Read-only.** `Location`, the ssh runner, `board --json`, remote items and tallies in the picker. You can see what the desktop is doing without leaving the laptop. Most of the plumbing, and useful on its own.
2. **Attach.** Wrapper sessions, `Open` for remote items, hopping into a remote workspace. The edge cases live here.
3. **Parity.** Remote preview, remote kill, `ls` and `ws` output, `wisp ws new` against a host.

## Open decisions

**What `ctrl-x` means on a remote item.** Killing the remote session stops the agent; killing only the wrapper walks away and leaves it running. Proposed: kill the remote session, so `ctrl-x` means the same thing everywhere and walking away stays what `esc` already does.

**Preview of a live remote session.** One ssh round trip per cursor move, or fetch only once the cursor rests. Proposed: debounce at roughly 150ms. The preview loader is already asynchronous and already discards results for a row the cursor has left.
