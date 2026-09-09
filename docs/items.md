# Items

What an item is, how it is named, and why the same piece of work found three ways is still one row.

---

## The shape

An item is a folder in the vault, exactly two levels deep.

```
working_items/
├── payments-api/
│   ├── payments-api.md            hub note: beside the items, not one of them
│   └── 1042-retry-backoff/
│       ├── orchestration.md       the manifest, for multi-repo items
│       ├── notes.md               yours; carries the done flag
│       └── .wisp-context.md       generated on open, safe to delete
└── _adhoc/
    └── vq-workspace/              work with no ticket behind it
```

`<repo>/<iid>-<slug>` for work with a ticket, `<repo>/<name>` for work without one, `_adhoc/<name>` for work with no repo either. `_adhoc` is a sentinel parent, not a repo: an item under it has no repo to infer and its session is notes-only. It is the fallback rather than the default: a bare name given to `wisp new` or `ctrl-n` is tied to a repo whenever the workspace can say which ([Commands](commands.md#wisp-new-nameurl---json)), and the picker asks when it cannot.

Only `notes.md` is guaranteed, created with the folder. Everything else is optional, and items in practice hold whatever else the work needed.

What a folder starts out holding is the workflow's to say: `item.seed` names a directory whose top-level files are copied into every new item, with `{item} {slug} {repo} {iid} {title} {date} {parent} {workspace} {vault}` filled in, never over a file that is already there. A seeded `notes.md` replaces the `# <slug>` stub; anything else sits beside it. Dotfiles and subdirectories are not copied, so a seed is the shape a folder starts in and not a way to put an `.envrc` where a shell will read it ([Workflows](workflows.md#the-keys)).

**Every subdirectory counts as an item**, not only the numbered ones. `wisp new <repo>/<name>` files an item under a repo without a ticket behind it, and requiring a leading number here meant wisp created those, opened them, and then left them out of its own list.

Three directory names are skipped as infrastructure: `.git`, `.claude`, `.obsidian`. The vault is usually both a git repo and a markdown vault.

---

## Identity

The stable identity of an item is `<repo>/<iid>` — the repo and the number, not the slug.

| name | identity | repo | slug |
|---|---|---|---|
| `payments-api/1042-retry-backoff` | `payments-api/1042` | `payments-api` | `1042-retry-backoff` |
| `_adhoc/vq-workspace` | `_adhoc/vq-workspace` | — | `vq-workspace` |
| `payments-api/no-iid-here` | `payments-api/no-iid-here` | `payments-api` | `no-iid-here` |

Slugs drift. A local folder is named by hand; a GitLab row is named from the title, cut to four words. The same ticket routinely exists under two spellings, and only the number deduplicates them correctly. An item with no number is its own identity.

The slug is the whole trailing element, number included, because it names worktrees and branches: `.worktrees/payments-api--1042-retry-backoff`, `feature/1042-retry-backoff`.

---

## State

Four values, and they are a ladder.

| | | |
|---|---|---|
| `+` | **gitlab** | on GitLab, nothing local |
| `○` | **folder** | a vault folder exists, no session |
| `●` | **live** | a tmux session is running |
| `?` | **needs input** | running, and the agent is waiting on a human |

Order matters because when the same item is found by more than one source, the highest wins. A live session always beats the vault folder it came from, and the vault folder beats the GitLab row.

---

## Done is not a fifth state

Closing an item out is a fifth thing an item can be, but not a fifth rung.

The four states are a ladder and merging keeps the highest, so a done item found again as a live session or a still-open GitLab row would have the flag overwritten by whichever source spoke last. Done is a **separate axis**: it says what you decided, not what the machine observed, and only the two together decide whether a row is worth showing.

It lives in `notes.md` frontmatter rather than `orchestration.md`, because every item has notes and only some have orchestration. An `_adhoc` item has no repos, so marking one finished would otherwise mean writing a manifest that describes nothing. Frontmatter in a note is also what a markdown vault's editor already understands, so it shows up there as a property rather than as stray text in the body.

```markdown
---
tags:
    - review
done: true
---

# 1042-retry-backoff

...
```

Reading it fails open: a missing, unreadable or malformed note is not done. Being unable to tell has to fall towards showing the row, because a closed-out item that reappears is a small annoyance and work that silently vanishes from the only list of it is not.

Writing it never deletes. The note is the writing, and a close that destroys it is one nobody would trust enough to use. The frontmatter is edited as a YAML node rather than parsed into a one-field struct and re-emitted, or a round trip would silently take your tags and aliases with it.

Set it with `ctrl-d` in the picker, `wisp done <item> -m '<line>'` from the shell, or by hand in your editor.

Closing out also writes the line saying what finished, under a dated heading, because separately the two did not happen. The flag was one keystroke and the write-up was a trip to an editor, and the result was a vault where every item carrying the flag had a note holding nothing but the flag. An item whose note is still empty is asked for a line before it closes; one you have already written something about closes immediately.

---

## Merging

The picker asks three sources and folds them into one list. What each contributes:

| source | knows |
|---|---|
| tmux sessions | name, state |
| the vault | name, folder state, done |
| GitLab | name, gitlab state, title |

They are merged in that order, and the rules are:

- **Name**: the first source to mention an identity wins, and is never overwritten. That is what makes a hand-chosen local slug beat the one derived from a GitLab title.
- **State**: the highest wins.
- **Title**: the first non-empty wins, so the GitLab row can fill in a title the local row never had.
- **Done**: sticky — kept if *any* source knew it. Only the vault row reads the note, so a session or a GitLab row merging over it carries "not done" and would otherwise reopen an item by arriving second.
- **Order**: the first list's ordering is the output ordering.

Hiding the closed-out ones happens where the list is displayed, not at the source. Filtering the vault rows before the merge would drop the row before the GitLab pass ran, and a still-open ticket would then re-add the item that had just been hidden.

---

## Names you type

`wisp new` and the picker's `ctrl-n` slugify each path segment: lowercase, `a-z0-9` plus `_` and `.`, everything else collapsed to a single dash, no truncation. You typed it and expect to see it back.

Dots survive because plenty of repos have one in the name. That is also why the traversal guard is explicit: any segment made only of dots is refused, so `wisp new ../thing` produces `..-thing` rather than writing a folder outside the vault entirely. A `/` inside a segment becomes a dash, so it cannot introduce a level.

Names derived from GitLab titles are slugified differently: lowercase, `a-z0-9` only, and **cut to the first four words**. That rule cannot change. It is what the bash version produced, and a different slug would make a second folder for work that already has one.

```
"Fix the thing that broke on prod"  →  fix-the-thing-that
"CVE-2026-1234"                     →  cve-2026-1234
```

---

## What a name has to denote

The picker only ever offers rows it found, so it can hand any of them straight to `open`. The command line cannot, and a typo there used to build a whole session around itself: a real session named after the typo, holding an agent that was told nothing, sitting in the list until someone killed it by hand.

So `wisp open` checks first, with three ways to be real and none of them a network call:

1. the vault folder exists,
2. a session is already running under that name, or
3. the repo half is a directory in this workspace — which is what a GitLab item looks like before it is opened for the first time.

For a remote workspace the check is deferred. The machine that owns the vault runs the same check on it, and it is the only one that can.
