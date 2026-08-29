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
      };
    }
    // flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.wisp = pkgs.callPackage ./package.nix { };
        packages.default = self.packages.${system}.wisp;

        # `nix develop` for contributors who do not use the direnv/default.nix path.
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [ go gopls go-tools tmux fzf glab ];
        };
      });
}
