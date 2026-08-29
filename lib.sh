# wisp shared library. Sourced into every subcommand by wisp.nix; not a standalone script.
#
# WS is set by the nix prelude (WISP_WORKSPACE, falling back to the baked workspacePath).
# PATH is pinned by pog's runtimeInputs, which is what keeps the GNU-vs-BSD assumptions
# below honest — see the find -mmin note in gitlab_items.

VAULT="$WS/working_items"
WT_ROOT="$WS/.worktrees"
PROVISION="$WS/.claude/scripts/provision-worktree.sh"
PREFIX="wisp_"
PROGRAM="${WISP_PROGRAM:-claude}"
CACHE="${TMPDIR:-/tmp}/wisp-gitlab-cache.json"
CACHE_TTL_MIN=15

GROUP="sinch/sinch-projects/voice/functions"
USERNAME="jadfis"

# fzf re-invokes wisp for its preview and its ctrl-x / ctrl-r bindings, so the script needs
# its own path. pog emits a single unwrapped file, so $0 is the real thing: absolute when
# invoked via PATH, relative when invoked as ./wisp. Normalise both.
SELF="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"

require_workspace() {
  [ -d "$VAULT" ] && return 0
  echo "wisp: workspace vault not found: $VAULT" >&2
  echo "    set WISP_WORKSPACE, or rebuild with a different workspacePath" >&2
  exit 1
}

# tmux session names cannot contain . or : - sinchfunctions.api would break.
sanitize() { printf '%s' "$1" | tr -c 'a-zA-Z0-9_-' '-'; }
sess_for() { printf '%s%s' "$PREFIX" "$(sanitize "$1")"; }
slug_of() { printf '%s' "${1##*/}"; }

# tmux target syntax is not uniform: the "=" exact-match prefix is accepted for session
# targets (has-session, attach-session, kill-session) but rejected by set-option/show-option
# and by capture-pane, whose -t is a target-PANE. Use "=" only where it is supported.
item_for() { tmux show-option -qv -t "$1" @wisp_item 2>/dev/null; }
live_sessions() { tmux ls -F '#{session_name}' 2>/dev/null | grep "^$PREFIX" || true; }

# --- sources -----------------------------------------------------------------

repos() {
  find "$WS" -mindepth 2 -maxdepth 2 -name .git 2>/dev/null | while read -r g; do
    d=${g%/.git}
    b=${d##*/}
    [ "$b" = "working_items" ] || printf '%s\n' "$b"
  done | sort -u
}

local_items() {
  # <repo>/<iid>-<slug> and _adhoc/<name>
  find "$VAULT" -mindepth 2 -maxdepth 2 -type d \
    -not -path '*/.git/*' -not -path '*/.claude/*' -not -path '*/.obsidian/*' \
    2>/dev/null | while read -r d; do
    rel="${d#"$VAULT"/}"
    case "$rel" in
      _adhoc/*) printf '%s\n' "$rel" ;;
      [0-9]* | */[0-9]*-*) printf '%s\n' "$rel" ;;
    esac
  done | sort -u
}

refresh_cache() {
  command -v glab >/dev/null || return 0
  if glab api graphql -f query="
  {
    group(fullPath: \"$GROUP\") {
      workItems(assigneeUsernames: [\"$USERNAME\"], state: opened, includeDescendants: true, first: 100) {
        nodes { iid title webUrl }
      }
    }
  }" >"$CACHE.tmp" 2>/dev/null; then
    mv "$CACHE.tmp" "$CACHE"
  else
    rm -f "$CACHE.tmp"
  fi
}

gitlab_items() {
  command -v jq >/dev/null || return 0
  # find -mmin, not stat: GNU coreutils stat (which is what nix gives us) reads -f as
  # "filesystem status" and prints to stdout, poisoning any arithmetic.
  if [ ! -f "$CACHE" ] || [ -n "$(find "$CACHE" -mmin "+$CACHE_TTL_MIN" 2>/dev/null)" ]; then
    refresh_cache
  fi
  [ -f "$CACHE" ] || return 0
  # Project and group items both use /-/work_items/N; only the /groups/ prefix marks a
  # group-level epic, which has no repo dir and is skipped.
  jq -r '.data.group.workItems.nodes[]
         | select(.webUrl | test("/groups/") | not)
         | select(.webUrl | test("/functions/(front-end/)?[^/]+/-/"))
         | [ (.webUrl | capture("/functions/(?:front-end/)?(?<r>[^/]+)/-/").r), .iid, .title ]
         | @tsv' "$CACHE" 2>/dev/null | while IFS=$'\t' read -r repo iid title; do
    [ -n "$repo" ] && [ -n "$iid" ] || continue
    slug=$(printf '%s' "$title" | LC_ALL=C tr '[:upper:]' '[:lower:]' |
      LC_ALL=C tr -c 'a-z0-9' ' ' | awk '{for(i=1;i<=NF&&i<=4;i++)printf "%s%s",(i>1?"-":""),$i}')
    printf '%s/%s-%s\n' "$repo" "$iid" "$slug"
  done
}

# <repo>/<iid> - the stable identity. Slugs are hand-chosen locally and derived from the
# title on GitLab, so they diverge; only this key dedups correctly.
key_of() { printf '%s' "$1" | sed 's|^\([^/]*\)/\([0-9][0-9]*\)-.*|\1/\2|'; }

# --- manifest ----------------------------------------------------------------

# The manifest is YAML frontmatter in orchestration.md, written by /orch. It records intent
# (repo, branch, base) and deliberately not worktree paths: a path is a cache, and a deleted
# worktree should be a cache miss rather than lost state.
#
# Emits: <repo>\t<branch>\t<base>, one line per repo.
manifest() {
  it="$1"
  f="$VAULT/$it/orchestration.md"
  if [ -f "$f" ]; then
    awk '
      /^---[[:space:]]*$/ { n++; if (n==2) exit; next }
      n==1 {
        if ($1 == "-" && $2 == "repo:") { if (r != "") print r"\t"b"\t"s; r=$3; b=""; s="" }
        else if ($1 == "branch:") b=$2
        else if ($1 == "base:")   s=$2
      }
      END { if (r != "") print r"\t"b"\t"s }
    ' "$f"
    return 0
  fi
  # No orchestration.md means a single-repo item: infer one entry from the folder's parent.
  parent="${it%%/*}"
  [ "$parent" = "_adhoc" ] && return 0
  printf '%s\t%s\t\n' "$parent" "feature/$(slug_of "$it")"
}

worktree_for() { printf '%s/%s--%s' "$WT_ROOT" "$1" "$(slug_of "$2")"; }

# Reprovision anything the manifest declares but disk lacks. provision-worktree.sh already
# handles branching from the remote default, copying default.nix/.envrc, direnv allow and
# the dependency install, so wisp does not duplicate any of that.
ensure_worktrees() {
  it="$1"
  manifest "$it" | while IFS=$'\t' read -r repo branch base; do
    [ -n "$repo" ] || continue
    wt=$(worktree_for "$repo" "$it")
    [ -d "$wt" ] && continue
    [ -d "$WS/$repo" ] || {
      echo "wisp: no such repo: $repo" >&2
      continue
    }
    [ -x "$PROVISION" ] || {
      echo "wisp: provisioning script missing: $PROVISION" >&2
      return 1
    }
    echo "--- provisioning $repo ($branch)"
    # --attach always: wisp's contract is "give me a worktree for this branch", and an item
    # reopened after cleanup has a live branch but no checkout. Without it, every
    # reconstitution fails on "branch already exists".
    #
    # This runs ONCE per worktree, never on re-attach: the guard above skips any worktree
    # already on disk, and ensure_worktrees is only reached when no session exists.
    #
    # No dependency install by default. nix/direnv still gives a working toolchain, and
    # node_modules for a monorepo like sinch-functions-runtime-node is 210M — too much to
    # pay for opening an item to read it. Set WISP_INSTALL=1 when you intend to build.
    set -- "$WS/$repo" "$(slug_of "$it")" "$branch" --attach
    [ -n "$base" ] && set -- "$@" --base "$base"
    [ -n "${WISP_INSTALL:-}" ] || set -- "$@" --no-install
    "$PROVISION" "$@" || true
  done
}

# --- context -----------------------------------------------------------------

# Per-item context: the repos in the manifest, and for each, its vault hub note and workspace
# doc. An item touching two repos pulls two repo contexts, not one per repo in the workspace.
context_file() { printf '%s/%s/.wisp-context.md' "$VAULT" "$1"; }

# The single-quoted printf formats below are markdown: every backtick is meant literally,
# so SC2016 ("expressions don't expand in single quotes") is exactly the intent.
# shellcheck disable=SC2016
write_context() {
  it="$1"
  out=$(context_file "$it")
  [ -d "$VAULT/$it" ] || return 0
  {
    printf '# Session context: %s\n\n' "$it"
    printf 'Generated by wisp. cwd is the workspace root, so every path below is relative to it.\n\n'
    printf '## This item\n\n'
    for f in orchestration.md notes.md; do
      [ -f "$VAULT/$it/$f" ] && printf -- '- `working_items/%s/%s`\n' "$it" "$f"
    done
    printf '\n## Repos in this item\n\n'
    manifest "$it" | while IFS=$'\t' read -r repo branch base; do
      [ -n "$repo" ] || continue
      printf -- '### %s\n\n' "$repo"
      printf -- '- branch `%s`' "$branch"
      [ -n "$base" ] && printf ' from `%s`' "$base"
      printf '\n'
      wt=$(worktree_for "$repo" "$it")
      [ -d "$wt" ] && printf -- '- worktree `%s`\n' "${wt#"$WS"/}"
      [ -f "$VAULT/$repo/$repo.md" ] && printf -- '- hub note `working_items/%s/%s.md`\n' "$repo" "$repo"
      [ -f "$WS/docs/$repo.md" ] && printf -- '- workspace doc `docs/%s.md`\n' "$repo"
      printf '\n'
    done
  } >"$out"
  printf '%s' "$out"
}

# --- status ------------------------------------------------------------------

# Best-effort, borrowed from claude-squad session/tmux/tmux.go:255. This is a hardcoded
# string from Claude Code's permission dialog and will break silently if that copy changes;
# the preview pane is the reliable signal.
needs_input() {
  tmux capture-pane -p -t "$1" 2>/dev/null |
    grep -qF "No, and tell Claude what to do differently"
}

# --- picker ------------------------------------------------------------------

candidates() {
  seen="" # keys already emitted, so an item never appears twice under two slugs
  while read -r s; do
    [ -n "$s" ] || continue
    it=$(item_for "$s")
    [ -n "$it" ] || it="$s"
    if needs_input "$s"; then printf '? %s\n' "$it"; else printf '%s %s\n' '●' "$it"; fi
    seen="$seen$(key_of "$it")
"
  done < <(live_sessions)
  # Local folders first: a hand-chosen slug always wins over the derived one.
  {
    local_items
    gitlab_items
  } | while read -r it; do
    [ -n "$it" ] || continue
    k=$(key_of "$it")
    printf '%s\n' "$seen" | grep -qxF "$k" && continue
    seen="$seen$k
"
    if [ -d "$VAULT/$it" ]; then printf '%s %s\n' '○' "$it"; else printf '+ %s\n' "$it"; fi
  done
}

preview() {
  it="$1"
  s=$(sess_for "$it")
  if tmux has-session -t "=$s" 2>/dev/null; then
    tmux capture-pane -p -e -J -t "$s" 2>/dev/null | grep -v '^[[:space:]]*$' | tail -n 40
    return 0
  fi
  # Manifest first: for a multi-repo item it is the most useful thing to see before opening.
  m=$(manifest "$it" 2>/dev/null)
  if [ -n "$m" ]; then
    printf 'repos:\n'
    printf '%s\n' "$m" | while IFS=$'\t' read -r repo branch base; do
      [ -n "$repo" ] || continue
      wt=$(worktree_for "$repo" "$it")
      if [ -d "$wt" ]; then mark="worktree ready"; else mark="needs provisioning"; fi
      printf '  %-30s %-42s %s\n' "$repo" "$branch" "$mark"
    done
    printf '\n'
  fi
  # Frontmatter is already rendered as the repos block above; showing it again wastes the pane.
  strip_fm() { awk 'NR==1 && /^---[[:space:]]*$/ {fm=1; next} fm && /^---[[:space:]]*$/ {fm=0; next} !fm' "$1"; }
  if [ -f "$VAULT/$it/notes.md" ]; then
    strip_fm "$VAULT/$it/notes.md" | head -n 30
  elif [ -d "$VAULT/$it" ]; then
    alt=$(find "$VAULT/$it" -maxdepth 1 -name '*.md' | head -1)
    if [ -n "$alt" ]; then
      printf '%s\n\n' "${alt##*/}"
      strip_fm "$alt" | head -n 30
    else
      printf 'folder exists, no markdown\n\n'
      ls -la "$VAULT/$it"
    fi
  else
    printf 'no folder yet - open GitLab item\n\n'
    [ -f "$CACHE" ] && jq -r --arg iid "$(printf '%s' "$it" | sed 's|.*/\([0-9]*\)-.*|\1|')" \
      '.data.group.workItems.nodes[] | select(.iid == $iid) | "\(.title)\n\(.webUrl)"' "$CACHE" 2>/dev/null
  fi
}

pick() {
  sel=$(candidates | fzf --ansi --height 80% --reverse \
    --header '● live  ? needs input  ○ folder  + gitlab   |   enter open  ctrl-x kill  ctrl-r refresh' \
    --preview "'$SELF' preview {2}" --preview-window 'right:60%:wrap' \
    --bind "ctrl-x:execute-silent('$SELF' kill {2})+reload('$SELF' candidates)" \
    --bind "ctrl-r:execute-silent(rm -f '$CACHE')+reload('$SELF' candidates)") || exit 0
  [ -n "$sel" ] || exit 0
  attach "$(printf '%s' "$sel" | awk '{print $2}')"
}

# --- open --------------------------------------------------------------------

attach() {
  it="$1"
  s=$(sess_for "$it")
  if ! tmux has-session -t "=$s" 2>/dev/null; then
    ensure_worktrees "$it"
    ctx=$(write_context "$it")

    # Window 0 is the agent, at the workspace root. One cwd sees the vault, docs, every repo
    # and every worktree, which is the whole point of the container model.
    if [ -n "$ctx" ] && [ -f "$ctx" ]; then
      tmux new-session -d -s "$s" -n agent -c "$WS" \
        "$PROGRAM \"Working on item $it. Read ${ctx#"$WS"/} first for the repos and notes in scope.\""
    else
      tmux new-session -d -s "$s" -n agent -c "$WS" "$PROGRAM"
    fi
    tmux set-option -t "$s" history-limit 10000 >/dev/null
    tmux set-option -t "$s" @wisp_item "$it" >/dev/null

    # One shell window per repo worktree, for builds and dev servers. Not agents.
    manifest "$it" | while IFS=$'\t' read -r repo branch base; do
      [ -n "$repo" ] || continue
      wt=$(worktree_for "$repo" "$it")
      [ -d "$wt" ] || continue
      tmux new-window -t "$s" -n "$(printf '%s' "$repo" | cut -c1-12)" -c "$wt" >/dev/null
    done
    # By name, not index: base-index may be 1, so "$s:0" is not reliably the first window.
    tmux select-window -t "$s:agent" >/dev/null 2>&1 || true
  fi
  # -u forces UTF-8 output to the client. Without it tmux only writes UTF-8 when the first
  # set variable of LC_ALL / LC_CTYPE / LANG contains "UTF-8", and on a bare SSH login none
  # of them is set (sshd has no AcceptEnv, so the client's locale is not forwarded either).
  # The symptom is specific and misleading: the pane buffer holds correct UTF-8, so
  # capture-pane looks fine, but every multibyte glyph reaches the terminal mangled.
  if [ -n "${TMUX:-}" ]; then tmux switch-client -t "=$s"; else tmux -u attach-session -t "=$s"; fi
}

# --- docs --------------------------------------------------------------------

docs() {
  repo="$1"
  [ -d "$WS/$repo" ] || {
    echo "wisp: no such repo: $repo" >&2
    exit 1
  }
  out="$WS/docs/$repo.md"
  if [ -f "$out" ]; then
    echo "exists: ${out#"$WS"/}"
    exit 0
  fi
  mkdir -p "$WS/docs"
  {
    printf '# %s\n\n' "$repo"
    printf '> Stub generated by wisp on request. Fill in from the repo itself.\n\n'
    printf '**Remote:** %s\n\n' "$(git -C "$WS/$repo" remote get-url origin 2>/dev/null || echo 'none')"
    printf '**Default branch:** %s\n\n' \
      "$(git -C "$WS/$repo" symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|origin/||' || echo '?')"
    printf '## Role\n\n## Layout\n\n## Build and test\n\n## Gotchas\n'
  } >"$out"
  echo "created: ${out#"$WS"/}"
}

list_sessions() {
  while read -r s; do
    [ -n "$s" ] || continue
    printf '%-46s %s\n' "$(item_for "$s")" "$s"
  done < <(live_sessions)
}

kill_session() {
  if tmux kill-session -t "=$(sess_for "$1")" 2>/dev/null; then
    echo "killed $1"
  else
    echo "no session for $1" >&2
  fi
}
