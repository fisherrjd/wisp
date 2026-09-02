# The picker

Type to filter, `enter` to open. Everything else is below.

```
› ledger                                  airbook ●3 · eldo ?1      2/8
▌ ? ledger-service/318-double-entry-audit  │  Edit file src/reconcile.ts
  + ledger-service/327-backfill-entries    │
                                           │  Do you want to make this edit?
──────────────────────────────────────────────────────────────────────
● live  ? needs input  ○ folder  + gitlab     enter open  ctrl-n new
```

Four regions: the prompt line, with the machine ring on the right and a match count; the list; the preview pane; the footer, carrying the glyph legend and the keys that apply right now.

---

## Modes

The picker has seven, and each one owns every key while it is open. For the lines you type into, that is what lets a URL or a path containing characters that are bindings elsewhere still type through cleanly.

| mode | what you see | enter with | leave with |
|---|---|---|---|
| **filter** | the item list | the default | `esc` quits |
| **new item** | a create line over the list | `ctrl-n` | `esc`, or `enter` to create |
| **workspaces** | the machine and workspace tree | `ctrl-w` | `esc`, `ctrl-w` |
| **new workspace** | a create line over the tree | `n` in the tree | `esc` |
| **add machine** | a create line over the tree | `a` in the tree | `esc` |
| **closing out** | a line over the list | `ctrl-d` on an item with an empty note | `esc` |
| **keys** | every binding, grouped | `ctrl-g` | `esc`, `ctrl-g`, `enter`, `q` |

`ctrl-c` quits from the item list and **cancels the mode** everywhere else. In the create lines it is the same as `esc`.

---

## Keys: the item list

| key | does |
|---|---|
| type | filter |
| `enter` | open the highlighted item |
| `↑` `ctrl-k` / `↓` `ctrl-j` | move the cursor |
| `backspace` | delete a character from the filter |
| `ctrl-u` | clear the filter |
| `ctrl-n` | create an item: a name, or a pasted GitLab link |
| `ctrl-d` | close the highlighted item out |
| `ctrl-t` | show the closed-out ones again |
| `ctrl-x` | kill the highlighted item's session |
| `ctrl-w` | open the workspace tree |
| `ctrl-r` | drop the GitLab cache and re-query |
| `ctrl-g` | the key list, every binding in one page |
| `esc` `ctrl-c` | quit, leaving everything running |

`ctrl-j` and `ctrl-k` rather than the emacs `ctrl-p` and `ctrl-n`, because `ctrl-n` is wanted for creating an item and splitting the pair across two idioms reads worse than moving both.

`ctrl-g` rather than the obvious `?`, because the filter line types: a bare `?` has to reach the query or an item with one in its name cannot be searched for. `ctrl-?` is worse than unavailable — most terminals send DEL for it, which is what `backspace` sends, and that is already bound.

The filter is fuzzy and matches the item **name** only, not its GitLab title. Matching re-orders the list by rank.

### `ctrl-d` and `ctrl-x` are not the same kind of finished

`ctrl-x` stops what is running and leaves the item. `ctrl-d` ends the work and leaves the list. `ctrl-x` already owns "stop the thing that is running", and this is the other half, "I am finished with this piece of work", which usually happens when nothing is running at all.

`ctrl-d` writes `done: true` into the item's `notes.md` frontmatter. `ctrl-t` brings the closed-out ones back into view, marked `✓`, and `ctrl-d` on one reopens it. A hidden thing needs a way back into view, or `ctrl-d` is a one-way door and an item marked finished by mistake is only recoverable by editing its note by hand.

On an item whose note is still empty, `ctrl-d` opens a line first:

```
 closing out   it was a caching bug in the router▏          one line on repo/1042-retry-backoff
──────────────────────────────────────────────────────────────────────
enter close it out   ctrl-d close it bare   esc cancel
```

`enter` writes the line under a dated heading and sets the flag in one pass. `ctrl-d` again closes it bare, for work there is nothing to say about. `esc` cancels and changes nothing.

The ask exists because the flag was one keystroke and the write-up was a trip to an editor, so the items that got closed out and the items that got written up turned out to be disjoint sets. An item you have already taken notes on closes immediately, with no line and no ceremony: the friction lands exactly where the record would otherwise be lost.

An item with a live session keeps its row either way, and keeps its `●`. Something is on the machine holding an agent, and hiding it would leave nothing pointing at the session.

Both reload the local list only. A kill changes tmux state and a close-out is a fact about this vault; re-querying GitLab for either would stall the list to learn nothing.

---

## Keys: the workspace tree

`ctrl-w` swaps the list for the level above it.

```
 workspaces                                  enter to go there
  airbook
▌   ○ near  (here)
  eldo  ?1
    ● work
    ✗ ghost
    ○ side
  gjallar
    ⚠ gjallar
```

| key | on a workspace | on a machine |
|---|---|---|
| `enter` | go there | go to its default workspace |
| `n` `ctrl-n` | make a workspace **on this machine** | same |
| `a` | add a machine | add a machine |
| `x` `ctrl-x` | forget the workspace | forget the machine and everything it holds |
| `↑` `k` / `↓` `j` | one row | one row |
| `←` `h` / `→` `l` | a whole machine | a whole machine |
| `esc` `ctrl-w` | back to the list | back to the list |

Plain letters, because nothing here types: the tree is a list, not a filter box. Chords would also be a lottery, since wisp lives inside tmux and whichever chord you have chosen as your prefix never arrives. `ctrl-a` is a common one and reaches nothing.

Machines are rows rather than decoration, because they are things you act on: going to one means its default workspace, forgetting one drops everything it holds, and a new workspace belongs to whichever machine the cursor is in. `n` reads the path on that machine, so there is no host prefix to remember and none to typo. The prompt says which.

`x` is **forget**, not kill: it edits the config and leaves every file and every session alone. The same key kills a session in the item list, which is a heavier thing, so the footer says "forget" here rather than "kill".

Nothing in the tree touches disk except making the vault and `.wisp.yaml` for a new workspace.

`←` and `→` exist because up and down walk one row, which on a machine holding several workspaces means several presses to get past it. They land on the first workspace of the next machine rather than the same slot within it: machines hold different numbers of workspaces, so there is no same position to keep.

---

## Glyphs

| item | means |
|---|---|
| `+` | on GitLab, nothing local yet |
| `○` | a vault folder, no session |
| `●` | a session is running |
| `?` | running, and the agent is waiting on a human |
| `✓` | closed out (only visible under `ctrl-t`) |

They are a ladder. When the same item is found by more than one source the highest wins, so a live session always beats the vault folder it came from and the vault folder beats the GitLab row.

`✓` is not a fifth rung. Done is a separate axis: it says what you decided, not what the machine observed, and it only replaces the glyph while nothing is running.

| workspace | means |
|---|---|
| `⚠` | could not be reached |
| `✗` | reached, but there is no vault there |
| `?` | holds an agent waiting on a human |
| `●` | holds something running |
| `○` | idle |

Unreachable is separate from missing on purpose. Both are unusable, but one is fixed by creating a directory and the other by fixing ssh, and a single glyph for both would send you after the wrong one.

The legend changes with the mode, because a `●` that means a running session and a `●` that means a workspace holding one are not the same claim.

---

## The header ring

```
› ▏                     near ●2 · eldo ?1 · eldo/side · box ⚠      4/9
```

**Machines**, not workspaces. The list itself is scoped to one workspace on purpose, so this is the only place the cross-workspace view survives, and the header has room for one level. A machine with an agent waiting is the thing worth seeing from a list that shows neither; which of its workspaces it is in is what `ctrl-w` is for.

The needs-input count displaces the plain live count when there is one, because it is the one that means go there now. An unreachable machine is marked rather than hidden: a machine missing from a list you wrote yourself reads as wisp losing it rather than as something to fix.

The ring is hidden entirely when there is only one workspace. Nothing to hop to, so it would be noise.

---

## Loading

Two phases, not one. Local candidates paint immediately; remote ones fold in when the network answers. Waiting for both before showing anything made opening the picker feel slow for the sake of the least important rows.

The fast path is filesystem and tmux only. The GitLab query runs off the UI goroutine, since it can block for most of a second on a cold cache and blocking the update loop would freeze typing. While it is out you get `…` where the count goes and an italic `loading…` in the list pane.

A remote failure is **shown, not swallowed**: it lands on the status line above the footer. An empty `+` section otherwise looks like having no assigned items rather than a broken query. The status line sits above the legend rather than replacing it, because a missing GitLab config persists for the whole session and swapping out the legend for it would trade one piece of permanently missing information for another.

---

## The preview pane

Whatever is most true about the item, in this order:

1. If a session is running, its live pane. Attached from here, so it is already rendering whatever the agent is doing, whether that agent is on this machine or another one.
2. If the workspace is remote, the far side's own preview, fetched over ssh.
3. Otherwise, a summary: the manifest's repos with `worktree ready` or `needs provisioning` against each, then the item's notes with frontmatter stripped.

With no `notes.md`, it falls back to the first `.md` in the item folder.

Lines are truncated rather than wrapped, and the block is padded to a fixed height. A wrapped line would make the preview taller than the pane and shove it out of alignment with the list beside it; a variable height would make the layout visibly shift as you move down the list. Tabs are expanded to four spaces before measuring, because a terminal renders a tab as up to eight cells while the width calculation counts it as one, which is what used to make the layout jump while scrolling.

Previews are fetched on every cursor move with no debounce. Over ssh that is cheap because the connection is multiplexed and held open for a minute, but it is one request per keystroke.

---

## Refusals

The picker declines four things out loud rather than silently, because a key that does nothing reads as a broken binding.

| | |
|---|---|
| killing your own session | `that is the session you are in; switch away first, or use wisp home` |
| forgetting this machine | `this machine is not something to forget; x drops a workspace, or a machine you added` |
| going to a workspace that is not there | `ghost does not exist yet: /Users/you/gone` |
| a create line missing a field | `give it a path: `<name> ~/somewhere`` |

The first is the difference between "kill that session" and "the picker vanished": tmux tears the client down along with its session, so killing your own from a popup looks exactly like wisp crashing rather than like the kill succeeding. Running `wisp home` keeps the picker in its own session, where the case cannot arise.

Errors from a create line keep your text so you can correct it in place. A missing directory is fixed by adding `-p`, which is one keystroke from there.

---

## Layout

The list is fixed at 40% of the width so long item names stay readable while the preview still gets the majority. Both panes derive their height from one number, which is what keeps them ending on the same row.

The footer **stacks rather than truncating** when the terminal is too narrow. That is the whole reason for leaving fzf, whose header could only ever truncate: each half wraps onto as many rows as it needs, and every row is counted, so the panes above never slide off the top.

### The footer is five hints, not a manifest

| Hint | Shown when |
|---|---|
| `enter open`, `ctrl-d done` | the cursor is on a row. Both act on the highlighted item, so neither means anything on an empty list |
| `ctrl-t show closed` | something has been closed out. It becomes `ctrl-t hide closed` while they are on screen |
| `ctrl-n new`, `ctrl-g keys`, `esc quit` | always |

The three verbs, the way out, and the way to everything else. `ctrl-x`, `ctrl-w`, `ctrl-r` and the tree's own letters are on the `ctrl-g` page, which exists so this line does not have to carry them.

Listing all eight came to 120 columns, wider than an ordinary terminal, so the stacking above happened to most people most of the time and the bar was the busiest thing on a screen whose whole point is a calm list. Five hints is 62 columns, which stays on one line beside the legend down to a 108-column terminal.

`ctrl-w` is the one that hurts to drop, since the workspace ring is half of what wisp is. The header already carries that: it names the other machines and tallies what is waiting on each. A hint repeating that the tree exists is not what makes it discoverable.

`ctrl-t` is the exception that stays conditional rather than moving to the page. It is the way back from a mistaken `ctrl-d`, so it cannot wait for someone to already know about it, and it arrives the moment there is something to come back to.

### `ctrl-g` is the whole vocabulary

A footer that only shows what applies needs somewhere the rest still lives. `ctrl-g` opens a page of every binding, grouped by what it acts on, and `esc` closes it.

It takes the whole body rather than floating over the panes. Compositing a box on top of two panes is a layer lipgloss does not have, and a page you are reading does not need to show you the list you are not reading.

The tree's plain letters are the reason this page exists as much as the shed hints are: `n`, `a` and `x` were previously discoverable only by already being in the tree, which is the one place the footer describing them is not on screen when you would want it.
