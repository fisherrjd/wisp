# Hooks

Status: **built, all four.** A hook is a key in a workflow bundle rather than a loose key in one file, wisp resolves `source:`, `context:`, `close:` and `provision:` to an absolute path across every layer, and it runs each of them at the point described below. [Workflows](workflows.md) is the reference for the bundle, the addressing and the precedence; this page stays because it holds the reasoning that picked the hook shape, and the contracts below are what a hook is actually handed.

Two of its decisions were changed on purpose. Both are marked where they are made, and [What changed, and why](#what-changed-and-why) at the end says what the alternative cost.

**`source:`, `context:` and `close:` are run the same way**, through one function. The command is the script path, with no shell around it, so the file has to be executable and carry its own `#!` line. Its working directory is the workspace root, never wherever wisp was invoked from, and `WISP_WORKSPACE` is set to the same path. stdout is captured, because for all three it is the answer. stderr is captured rather than inherited, and on a non-zero exit its **last line** becomes what the hook gets to say, prefixed with the script's basename: one line, because it goes somewhere with room for one.

`provision:` is the odd one, and it is odd because it is older than the rest. It is invoked directly rather than through that function: same working directory, but **no `WISP_WORKSPACE`**, a fixed argument list instead of stdin, and stdout and stderr inherited rather than captured, because it runs in a window of its own where a live build log is the point. A failure is logged to that window and the other repos are still attempted, rather than the whole open being abandoned. Do not write a `provision` script that reads `WISP_WORKSPACE`; it gets `<repo-path>` as its first argument instead.

**There is no timeout on any of them.** wisp waits for the process, with no deadline and nothing to interrupt it but `ctrl-c`. That is a deliberate omission rather than an oversight: a deadline wisp picked would kill a slow-but-working tracker query on a bad network, and a `source` hook that is merely slow is a much more likely thing than one that hangs. The cost is real and is yours to carry: a `source` hook that never returns hangs the picker's refresh, and a `close` hook that never returns sits between `ctrl-d` and the row disappearing.

---

## The problem

Written before any of this existed, and kept in that tense: this is what the code looked like, and the argument it produced is what the contracts below still are. Four of the six rows are now workflow keys, one per line: where items come from, what the agent is told, the session layout and the needs-input signal. The other two are still compiled in. `done: true` in `notes.md` frontmatter is still what finishing means, and the `close` hook runs beside that rather than replacing it; `repos: [{repo, branch, base}]` is still the manifest format, and no key changes it.

wisp worked for one person's workflow, and that was not a figure of speech. Six decisions were compiled in, and every one of them is somebody's preference wearing the clothes of a fact:

| | then | whose choice |
|---|---|---|
| where items come from | a GitLab GraphQL query filtered to `assigneeUsernames` | mine |
| what the agent is told | a Go string builder naming `orchestration.md` and `notes.md` literally | mine |
| the session layout | window 0 `agent` at the workspace root, one shell per worktree | mine |
| what finishing means | `done: true` in `notes.md` frontmatter | mine |
| the manifest format | `repos: [{repo, branch, base}]` in `orchestration.md` | mine, and `/orch` writes it |
| the needs-input signal | grepping the pane for a string from Claude Code's permission dialog | mine, and borrowed |

Two things are already yours to change: `program:`, the command in the agent window, and `provision:`, a path to a script. Everything else means wisp is a tool for one vault.

The GitLab one is the sharpest. Someone on GitHub, Jira, Linear or a text file gets an empty `+` section and a message about `gitlab.group`, which is wisp telling them their tracker is misconfigured when the truth is that wisp only knows one.

---

## The principle

**Name a contract, shell out, stay out of it.**

This is not a new idea here. It is what `provision:` already does, and the comment on it says why:

> It shells out rather than driving git directly: `provision-worktree.sh` already handles branching from the remote default, copying `default.nix` and `.envrc`, `direnv allow` and the dependency install, and duplicating that here would be a second source of truth.

That argument generalises. wisp knows about items, sessions, worktrees and a picker. It does not need to know about GitLab, or about what a good agent briefing says, or about where your team writes down what it learned. Those belong to whoever owns the workspace, and a workspace already has a file for saying so.

So: a small set of named hooks, each a script path in `.wisp.yaml`, each with a fixed argument list and a documented stdout. No plugin loader, no Go interface to implement, no DSL in the config. A hook is a program, and a program is something anyone can write in bash in ten minutes and test by running it.

Hooks live in `<workspace>/.wisp.yaml` rather than the user config, for the same reason the GitLab keys do: the workspace is the unit that owns a vault, a tracker and a set of conventions, and its config should travel with it.

> **Changed.** That is right for the tracker and wrong for everything else. A hook may now be set in a bundle, in either config file, or in an item, and it resolves per key across all of them. See [What changed, and why](#what-changed-and-why).

---

## `source:` — where items come from

Replaces the GitLab query. This is the one that decides whether wisp is usable by anyone else at all.

```yaml
hooks:
    source: bin/items.sh    # in a bundle; or `source:` at the top of a config file
```

**Two modes, one program**, because it is one piece of knowledge:

```
items.sh                  no args, lists open work as JSONL
items.sh --url <url>      resolves one URL, prints one object, or nothing
```

The second mode is the one a first draft of this forgot. Listing your work and starting an item by pasting its link are separate code paths, and a source that only replaced the first would leave a GitHub user able to see their tickets and unable to open one.

### Listing

No arguments. Prints one JSON object per line:

```json
{"name": "payments-api/1042-retry-backoff", "title": "Retry with backoff on 502"}
{"name": "webhooks-worker/88-drop-dead-letters", "title": "Drop dead letters after 7 days"}
```

`name` is required and must be `<parent>/<child>`, the same two-level shape the vault uses. `title` is optional and fills the row's description when the local slug differs.

**JSON per line rather than one array**, because a long list should stream, and because a truncated write should cost you the last row rather than the whole response.

A line that will not parse, or that names something that is not a legal item, is **skipped rather than failing the batch**: one bad row from a tracker must not empty the picker. Legal means it passes the same containment check everything else does and has exactly two non-empty levels, because a source hook is a program someone else wrote producing names that become directories. A workflow may decide where names come from; it may not decide what a name is allowed to be.

### Resolving a URL

```
$ wisp new https://gitlab.example.com/g/repoa/-/issues/7
repoa/7-from-a-link
```

Called as `<script> --url <url>`, with no stdin. It prints one object in the same shape, or nothing at all when it has no opinion about this link, which is a normal state rather than a failure: not every tracker can resolve every link. A non-zero exit means the same thing, and wisp says so in its own words, naming the script so the next place to look is obvious:

```
$ wisp new https://example.com/nope
wisp: ~/.config/wisp/workflows/hooked/bin/items.sh did not recognise that link (items.sh: exit status 1)

name the item yourself instead:
  wisp new <repo>/<name>
```

### What wisp keeps

Caching stays wisp's job. The hook is called when the cache is older than `gitlab.cache_ttl_min` or when `ctrl-r` drops it, exactly as the GitLab query is. A hook that wants to be cheap can be cheap; a hook that wants to be slow does not have to think about it. Moving the TTL into every hook would make `ctrl-r` mean something different for each one.

The cache is a separate file from the GitLab one, namespaced by workspace the same way, so switching a workspace from one to the other cannot serve the other's rows out of a warm cache. That the TTL is still spelled `gitlab.cache_ttl_min` is a leftover, and one of the places `gitlab.*` has not yet stopped being a top-level concern.

Merging stays wisp's job too. Items from the hook arrive at `StateRemote`, the lowest rung, so a live session or a vault folder still wins and a hand-chosen local slug still beats the name the hook produced. Nothing about identity changes.

### Failure

A non-zero exit is a **state, not an exception**: the local items still paint, and the last line of stderr is what surfaces.

```
source: items.sh: curl: (6) Could not resolve host: gitlab.example.com
```

An empty source and a broken one look identical otherwise, and the silent version of that already cost one real debugging session. A refresh that fails while a cache exists falls back to the stale cache rather than emptying the list, which is the same bargain the GitLab source has always made.

### The fallback

With no `source:` set, the built-in GitLab source runs, unchanged. Existing configs keep working and nobody has to write a script to get what they already have.

If both are set, **the hook wins and wisp says nothing**. That is worth stating plainly because it is the one place the "an empty source and a broken one must look different" rule is not applied to itself: a workspace with `gitlab.group` filled in and a `source` hook somewhere up the workflow stack quietly stops querying GitLab, and the only way to see that is `wisp workflow`, where the `source` row names the script and the layer it came from. Merging the two lists would be worse, but a line saying which one is off would be better than neither.

A remote workspace's source is the far side's business entirely: this end asks for its board and never learns whether a hook was involved.

---

## `context:` — what the agent is told

Replaces the hardcoded context file. Right now an agent's entire briefing is a Go template naming two files in an order I chose, and someone driving aider, codex or a bare shell wants different words.

```yaml
context: .wisp/context.sh
```

Given the item on stdin as JSON, so the script does not have to re-derive anything wisp already knows:

```json
{
  "item": "payments-api/1042-retry-backoff",
  "workspace": "/Users/you/work",
  "vault": "working_items",
  "dir": "working_items/payments-api/1042-retry-backoff",
  "repos": [
    {
      "repo": "payments-api",
      "branch": "feature/1042-retry-backoff",
      "base": "main",
      "worktree": ".worktrees/payments-api--1042-retry-backoff",
      "ready": false
    }
  ]
}
```

Prints the body of `.wisp-context.md` on stdout.

`ready` is the field that matters and the one a naive implementation forgets: provisioning runs in the background, so a worktree is listed whether or not it exists yet, and the agent needs to know where the checkout is *going to be*. `worktree` is relative to the workspace root for the same reason `cwd` is. wisp rewrites the file when provisioning finishes, and calls the hook again then.

Everything downstream is unchanged: wisp writes the file, inlines it into the startup prompt under the 8 KB bound, and points at the path above it.

### Failure

**Fall back to the built-in template and log it.** A session that will not open because a docs script has a syntax error is a worse outcome than a session that opens with a generic briefing. The error goes to stderr, beside whatever wisp was doing:

```
wisp: context hook failed, using the built-in briefing: context.sh: .../bin/context.sh: line 2: nope: command not found
```

A hook that exits zero and prints **nothing** counts as a failure too, and falls back the same way, saying so in as many words:

```
wisp: context hook failed, using the built-in briefing: .../bin/context.sh printed nothing
```

An empty briefing and a working one are not distinguishable downstream, and a session whose agent was told nothing at all is the outcome the fallback exists to prevent.

---

## `close:` — what closing out does

Runs when an item is closed out, before the flag is set.

```yaml
hooks:
    close: bin/close.sh
```

```
close.sh <item> [<line>]
```

The line is **omitted rather than passed empty** when there is none, so `$2` being set is how a script tells "closed with a write-up" from "closed bare", which is a distinction wisp itself already makes.

It runs on the way out only. Reopening an item with `wisp done --undo` is undoing a decision, and a hook that could block that would make a mistake permanent.

This is the hook with a real user waiting for it. There are currently four prose copies of one close-out workflow — in `working_items/CLAUDE.md`, in a `kb-harvester` persona, in a `reconcile` skill and inside `/orch` — none of which wisp knows about, and one of which says to delete the item folder, which `wisp done` deliberately never does. A script wisp calls collapses four descriptions into one implementation.

### It may refuse

A non-zero exit **aborts the close**. The item stays in the list and the flag is not set.

That is the whole point rather than a safety afterthought. `reconcile`'s existing rule is that an item may not be dropped until its content has been harvested, and nothing else enforces it; a hook that can say no is the enforcement. The last line of stderr becomes the message, behind the script's name:

```
$ wisp done repoa/42-do-a-thing
wisp: close-out refused: close.sh: nothing posted to gitlab yet for #1042
```

Which means the hook has to be fast and has to fail clearly, because it now sits between a keystroke and a row disappearing.

### Ordering

The hook runs first, then wisp writes the line and the flag in the single atomic write it already does. The other order would leave an item marked finished whose harvest failed, which is the exact state the flag is supposed to rule out.

---

## What must not become a hook

Two things, and both would break quietly rather than loudly.

**`Item.Key()`.** Identity is `<repo>/<iid>` and that is what makes the same ticket found in a session, a folder and a source into one row. A configurable identity is a configurable dedup, and the failure mode is the picker showing your work twice under two spellings with no error anywhere.

**`slugify`'s four-word truncation.** It matches what the bash version produced, so existing vault folders keep deduplicating against their remote counterparts. Changing it makes a second folder for work that already has one, and nothing notices.

Both are identity rather than preference. A hook that could change them would be a hook that could corrupt a vault by being slightly different.

The session layout is a third candidate I would leave alone for now, but for a weaker reason: it is a list in config rather than a script, so it does not fit the shape above, and until the three hooks here are real it is not clear whether it wants to be a hook at all.

> **Changed.** The weak reason was the right observation and the wrong conclusion. Layout is not hook-shaped, and it did not need to be: it is declarative, so it landed as a `layout:` key in the bundle rather than as a fourth script. See [What changed, and why](#what-changed-and-why).

---

## Remote workspaces

Nothing to design. A remote workspace's hooks run on the machine that owns it, because that machine runs its own `Load`, reads its own `.wisp.yaml` and answers `board --json` for itself. The near side asks a question and gets rows back; it never learns that a hook was involved.

That falls out of the existing design rather than being added to it, and it is worth stating because the opposite arrangement — hooks configured here, run against a filesystem over there — would be wrong in a way that is not obvious until it fails.

The wire is unaffected. `BoardJSON` already carries a `note` for a source that is off or broken, which is exactly what a failing `source:` needs.

---

## Order of work

Superseded, and kept because the test at the end of it is not. All three shipped together, behind the bundle and the resolution; [What changed, and why](#what-changed-and-why) says why that order won. What follows is the plan it replaced.

**`source:` alone, shipped, before either of the others.**

It is the one that changes who can use wisp. It is self-contained: one function with two call sites, one of which is already asynchronous and already handles the failure path. And it proves the hook shape — argv, stdout, caching, how a failure surfaces — on the smallest surface available, before that shape is committed to in two more places.

`context:` second, because it is the next most obviously somebody-else's-choice, and because a wrong answer there is recoverable by deleting a config line.

`close:` last, because it is the only one that can refuse an action, and a hook that can block a keystroke wants the other two to have already settled what a hook is.

### What would make each one wrong

That list of three is the acceptance test, and it outlived the order it was written for. All three hold in what shipped.

- **`source:`** if the failure path empties the list instead of annotating it. The whole value of the GitLab source's current design is that a broken query and an empty one look different.
- **`context:`** if a broken hook stops a session opening. Falling back is not a compromise, it is the requirement.
- **`close:`** if the refusal is silent, or if the flag gets written before the hook runs.

The place the same standard is not applied is one layer up: a `source` hook silently switches the built-in GitLab source off, and a workspace with `gitlab.group` filled in gets no word that its query has stopped running. It is named under [The fallback](#the-fallback) rather than quietly left out.

---

## What changed, and why

Four decisions on this page were kept without change, and they are the load-bearing ones:

- **Name a contract, shell out, stay out of it.** A hook is a program, not a Go plugin, not a shared object, not a DSL in YAML.
- **Identity is not configurable.** `Item.Key()` and the four-word slug cut stay compiled in.
- **Failure is a state, not an exception.** A broken source annotates the list and never empties it; a broken context hook falls back to the built-in briefing rather than refusing to open a session. That discipline generalised: a workflow that will not load now costs you its keys rather than your session.
- **Remote workspaces need no design.** Hooks run on the machine that owns the workspace, because that machine runs its own load and answers `board --json` for itself.

Two were changed.

**Scope: hooks are no longer workspace-only.** The argument above is right about the tracker and wrong about the rest. How you like your session laid out and what you like your agent told are properties of *you*, and a design where the answer lives only in a checked-in per-workspace file gets that backwards: it makes the thing that should follow you between workspaces the one thing that cannot. The cost of keeping it would have been a per-workspace file edit for every workspace you open, forever, to say the same thing each time. So `source:`, `context:`, `close:` and `provision:` are now keys of a workflow bundle, resolvable from the bundle, either config file, or an item, per key. The workspace file still wins over the user file, which is the ordering this page's argument actually wanted.

One piece of the original scope survives intact and is now a rule rather than a habit: **`source` is workspace-only**, and an item that sets it is refused. Not because a workspace owns the tracker, but because an item cannot have an opinion about where items come from: it does not exist until `source` has run.

**Session layout: deferred as "not hook-shaped", now built as a key.** The observation was correct and the conclusion was not. Layout is a list in config rather than a script, so it does not fit the hook shape, but that is an argument for it not being a hook, not an argument for leaving it compiled in. It is also the single most visible "that is not how I work" surface wisp has, the fix is declarative, and it needs no new execution contract, which makes it the *cheapest* of the lot rather than the one to do last. Waiting for the three hooks here to be real would have cost the highest-payoff change the longest wait, for no information it would have produced.

A third thing moved but is not a reversal: `status.needs_input`, the pane string that decides the `?` state. It was not on this page at all, and it belongs in the same bundle for the same reason. Matching a literal line out of Claude Code's permission dialog means anyone driving aider, codex or a bare shell has a state that can never fire.

The order of work above was also superseded. It is ordered by "who can use wisp at all", which puts `source:` first; [Workflows](workflows.md) is ordered by "whose workflow is it", which puts the bundle and resolution first so no hook has to be retrofitted onto them afterwards. Both are defensible and the second one shipped.

---

## What this deliberately is not

No plugin loader, no shared objects, no Go interface anyone implements, no registry, no lifecycle. Those buy dynamic dispatch, which nothing here needs, and cost a versioned API across a boundary that already has one version boundary too many.

A hook is a program. It can be written in anything, tested by running it, and debugged by reading its output. That is the same deal `provision-worktree.sh` has had since before this was Go, and the reason it has never needed a second look.
