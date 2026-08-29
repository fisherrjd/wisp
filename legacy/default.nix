{ lib
, pog
, bash
, coreutils
, findutils
, fzf
, gawk
, git
, glab
, gnugrep
, gnused
, jq
, tmux
  # Absolute path to the SinchFunctions workspace root (the container: repos, docs,
  # .worktrees and the working_items vault). Overridable at runtime with WISP_WORKSPACE;
  # this is only the default baked into the script.
, workspacePath ? "/Users/jadfis/voice/functions"
}:

let
  version = "0.1.3";

  runtimeInputs = [
    bash
    coreutils
    findutils
    fzf
    gawk
    git
    glab
    gnugrep
    gnused
    jq
    tmux
  ];

  # pog does not set any shell options of its own, so wisp's `set -euo pipefail` has to be
  # stated here. WS is the one value that must come from Nix rather than the library file.
  prelude = ''
    set -euo pipefail
    WS="''${WISP_WORKSPACE:-${workspacePath}}"
  '';

  # readFile, not an inline '' string: it keeps the ~300 lines of bash in a real .sh file
  # where shellcheck, shfmt and editor tooling still work, and it means the dense awk and
  # jq blocks need no Nix escaping. Verified against manifest()'s awk, the worst case.
  library = builtins.readFile ./lib.sh;

  # Every subcommand gets the whole library. It is one bash file either way, so there is
  # nothing to gain by slicing it, and shared helpers stay in one place.
  cmd = body: prelude + library + "\n" + body + "\n";

  # Items are <repo>/<iid>-<slug> vault folders. Completing them is the whole reason the
  # picker exists, so `wisp open <TAB>` should reach the same set without opening fzf.
  itemCompletion = pog.completions.dynamic {
    runtimeInputs = [ coreutils findutils gnugrep ];
    script = ''
      vault="''${WISP_WORKSPACE:-${workspacePath}}/working_items"
      [ -d "$vault" ] || exit 0
      find "$vault" -mindepth 2 -maxdepth 2 -type d \
        -not -path '*/.git/*' -not -path '*/.claude/*' -not -path '*/.obsidian/*' \
        2>/dev/null | while read -r d; do
        rel="''${d#"$vault"/}"
        case "$rel" in
          _adhoc/* | [0-9]* | */[0-9]*-*) printf '%s\n' "$rel" ;;
        esac
      done | sort -u
    '';
  };

  repoCompletion = pog.completions.dynamic {
    runtimeInputs = [ coreutils findutils ];
    script = ''
      ws="''${WISP_WORKSPACE:-${workspacePath}}"
      find "$ws" -mindepth 2 -maxdepth 2 -name .git 2>/dev/null | while read -r g; do
        d=''${g%/.git}
        b=''${d##*/}
        [ "$b" = "working_items" ] || printf '%s\n' "$b"
      done | sort -u
    '';
  };

  itemArgument = {
    name = "item";
    description = "vault item, as <repo>/<iid>-<slug> or _adhoc/<name>";
    completion = itemCompletion;
  };
in
# pogFn accepts no `meta`, so it is layered on afterwards via overrideAttrs.
(pog {
  name = "wisp";
  description = "item-centric tmux session picker for the SinchFunctions workspace";
  inherit version runtimeInputs;

  commands = [
    {
      # pog has no built-in --version, and a stale activation has to stay diagnosable
      # (this is why the old derivation asserted --version in its installCheck).
      name = "version";
      description = "print the wisp version";
      script = "printf 'wisp %s\\n' '${version}'";
    }
    {
      name = "pick";
      # Bare `wisp` is the picker. pog dies on an unrecognised first positional, so unlike
      # the old hand-rolled dispatch there is no `wisp <item>` fallthrough — that is `wisp open`.
      default = true;
      description = "fuzzy-pick an item: live sessions, vault folders, open GitLab items";
      script = cmd ''
        require_workspace
        pick
      '';
    }
    {
      name = "open";
      aliases = [ "o" ];
      description = "open an item: reprovision missing worktrees, assemble context, attach";
      arguments = [ itemArgument ];
      script = cmd ''
        require_workspace
        [ $# -ge 1 ] || die "usage: wisp open <item>" 2
        attach "$1"
      '';
    }
    {
      name = "ls";
      description = "list live sessions";
      script = cmd ''
        require_workspace
        list_sessions
      '';
    }
    {
      name = "kill";
      description = "kill the session for an item";
      arguments = [ itemArgument ];
      script = cmd ''
        require_workspace
        [ $# -ge 1 ] || die "usage: wisp kill <item>" 2
        kill_session "$1"
      '';
    }
    {
      name = "docs";
      description = "stub docs/<repo>.md if it does not exist yet";
      arguments = [{
        name = "repo";
        description = "repo directory in the workspace";
        completion = repoCompletion;
      }];
      script = cmd ''
        require_workspace
        [ $# -ge 1 ] || die "usage: wisp docs <repo>" 2
        docs "$1"
      '';
    }
    {
      name = "repos";
      description = "list workspace repos";
      script = cmd ''
        require_workspace
        repos
      '';
    }
    {
      # Hidden: fzf calls these back, they are not for humans. pog keeps hidden commands
      # invocable but omits them from help and completion.
      name = "preview";
      hidden = true;
      description = "render the fzf preview pane for an item";
      arguments = [ itemArgument ];
      script = cmd ''
        require_workspace
        preview "''${1:-}"
      '';
    }
    {
      name = "candidates";
      hidden = true;
      description = "emit the fzf candidate list";
      script = cmd ''
        require_workspace
        candidates
      '';
    }
  ];
}).overrideAttrs (_: {
  meta = {
    description = "Item-centric tmux session picker for the SinchFunctions workspace";
    license = lib.licenses.mit;
    mainProgram = "wisp";
    platforms = lib.platforms.unix;
  };
})
