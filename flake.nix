{
  description = "wisp - one work item, one tmux session";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    # The overlay is system-independent, so it lives outside eachDefaultSystem. Consumers add it
    # to their pkgs and get `wisp` alongside everything else.
    {
      overlays.default = final: _prev: {
        wisp = final.callPackage ./package.nix { };
        # The docs site is an output too, so whoever serves it gets it from the same revision as
        # the binary rather than keeping a copy that has to be remembered about.
        wisp-docs = final.callPackage ./docs/site/package.nix { };
      };
    }
    // flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.wisp = pkgs.callPackage ./package.nix { };
        packages.wisp-docs = pkgs.callPackage ./docs/site/package.nix { };
        packages.default = self.packages.${system}.wisp;

        # `nix develop` for contributors who do not use the direnv/default.nix path.
        devShells.default = pkgs.mkShell {
          # nodejs is for docs/site, the vite app that renders docs/*.md
          packages = with pkgs; [ go gopls go-tools tmux fzf glab nodejs ];
        };
      });
}
