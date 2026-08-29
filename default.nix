{ pkgs ? import
    (fetchTarball {
      # nixup: pin=jpetrucciani/nix;
      name = "jpetrucciani-2026-08-29";
      url = "https://github.com/jpetrucciani/nix/archive/2279da7e6fe31b0c54ec2174b5fe9a4d2f9e77a7.tar.gz";
      sha256 = "1sfyfc59r0kh8g4iizi29rsxhbbmgl644v17bq1pq9anadwz5h7m";
    })
    { }

}:
let
  name = "wisp";

  envVars = {
    NIXUP = "0.0.15";
  };
  tools = with pkgs; {
    cli = [
      jfmt
      nixup
    ];
    go = [
      go
      go-tools
      gopls
    ];
    scripts = pkgs.lib.attrsets.attrValues scripts;
  };

  scripts = with pkgs; { };
  paths = pkgs.lib.flatten [ (builtins.attrValues tools) ];
  env = pkgs.buildEnv {
    inherit name paths; buildInputs = paths;
  };
in
(env.overrideAttrs (old: {
  inherit name;
  env = (old.env or { }) // envVars;
})) // { inherit scripts; }
