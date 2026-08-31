{ lib
, runCommand
}:

# One hand-written page rather than a generator. The markdown in docs/ is the reference and this
# is the front door; mdBook or similar would add a build dependency and a lot of chrome to serve
# less than it ships.
#
# It lives here rather than in the config repo that deploys it, because a page describing wisp
# and the wisp it describes should move together. The copy that used to live over there drifted
# until it documented a version with no workspaces, no hosts and no close-out, and nothing said so.
runCommand "wisp-docs"
{
  meta = {
    description = "Static documentation site for wisp";
    license = lib.licenses.mit;
    platforms = lib.platforms.all;
  };
} ''
  mkdir -p $out
  cp ${./index.html} $out/index.html
''
