{ lib
, buildGoModule
, makeWrapper
, tmux
, git
, glab
}:

buildGoModule rec {
  pname = "wisp";
  version = "0.5.0";

  src = lib.cleanSource ./.;

  vendorHash = "sha256-mz/2IJrWk/02l20SnWIwXEKhqkoTYNSpoA1i62Bda5M=";

  ldflags = [ "-s" "-w" ];

  nativeBuildInputs = [ makeWrapper ];

  # wisp shells out to tmux for every session operation and to git via the workspace's
  # provisioning script. glab is optional: without it the remote source is simply empty.
  postInstall = ''
    wrapProgram $out/bin/wisp \
      --prefix PATH : ${lib.makeBinPath [ tmux git glab ]}
  '';

  meta = {
    description = "Item-centric tmux session picker: one work item, one session";
    homepage = "https://github.com/fisherrjd/wisp";
    license = lib.licenses.mit;
    mainProgram = "wisp";
    platforms = lib.platforms.unix;
  };
}
