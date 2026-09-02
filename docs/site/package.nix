{ lib
, buildNpmPackage
}:

# The site is a Vue 3 + Vite app, after fisherrjd/app-template: same stack, same
# dusk palette, same token wiring. What it is not is a second copy of the docs.
# The reference pages are docs/*.md bundled straight into the build, so the page
# and the markdown cannot disagree — the copy that used to live in the config
# repo that deploys it drifted until it documented a version with no workspaces,
# no hosts and no close-out, and nothing said so.
#
# It lives here rather than over there for the same reason: a page describing
# wisp and the wisp it describes should move together.

let
  # The same string the binary reports, read out of the binary's own source.
  # A docs package that can carry a version wisp does not have is the drift
  # this whole arrangement exists to stop.
  versionLine = lib.findFirst (line: lib.hasPrefix "const Version = " line) null
    (lib.splitString "\n" (builtins.readFile ../../internal/wisp/remote.go));
in

buildNpmPackage {
  pname = "wisp-docs";
  version = lib.removeSuffix "\"" (lib.removePrefix "const Version = \"" versionLine);

  # docs/ for the site and the markdown it renders, plus remote.go, which is
  # where the version and wire number in the header come from. Nothing else in
  # the repository changes this output, so nothing else should rebuild it.
  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.difference
      (lib.fileset.unions [
        ../../docs
        ../../internal/wisp/remote.go
      ])
      (lib.fileset.unions [
        (lib.fileset.maybeMissing ../../docs/site/node_modules)
        (lib.fileset.maybeMissing ../../docs/site/dist)
      ]);
  };

  sourceRoot = "source/docs/site";

  # Update with:
  #   nix run nixpkgs#prefetch-npm-deps -- docs/site/package-lock.json
  npmDepsHash = "sha256-6TWRFzfsK7xhONv+vqdR87eYFeiKfMHKjOMCkhUjY84=";

  # The default install phase wants an npm package to link; this is a directory
  # of static files.
  installPhase = ''
    runHook preInstall

    mkdir -p $out
    cp -R dist/. $out/

    # vue-router runs on history, not hashes, so /docs/commands has to be a real
    # path on whatever serves this. One copy of index.html per route is the whole
    # of the SPA fallback, and it works on a host with no rewrite rules at all.
    # Derived from the markdown, like the routes themselves, so a new page needs
    # nothing here.
    install -D $out/index.html $out/docs/index.html
    for md in ../*.md; do
      install -D $out/index.html "$out/docs/$(basename "$md" .md)/index.html"
    done
    # for hosts that prefer a 404 document to a rewrite
    install -D $out/index.html $out/404.html

    runHook postInstall
  '';

  meta = {
    description = "Documentation site for wisp";
    homepage = "https://github.com/fisherrjd/wisp";
    license = lib.licenses.mit;
    platforms = lib.platforms.all;
  };
}
