package wisp

import "testing"

// --all is scoped to the workspace unless told otherwise, adopts untagged sessions the way the
// default workspace does, and never lists the session it is running in.
func TestSelectKillsScopesAndSparesTheCurrentSession(t *testing.T) {
	all := []Session{
		{Name: "wisp_github_a", Item: "a", WS: "github"},
		{Name: "wisp_github_b", Item: "b", WS: "github"},
		{Name: "wisp_lab_c", Item: "c", WS: "wisp-lab"},
		{Name: "wisp_old_d", Item: "d", WS: ""}, // predates the workspace tag
	}
	names := func(ss []Session) string {
		out := ""
		for _, s := range ss {
			out += s.Item
		}
		return out
	}
	if got := names(selectKills(all, "github", "github", "wisp_github_b", false)); got != "ad" {
		t.Errorf("this workspace: got %q, want a and the adopted d, never the current b", got)
	}
	if got := names(selectKills(all, "wisp-lab", "github", "", false)); got != "c" {
		t.Errorf("another workspace: got %q, want c", got)
	}
	if got := names(selectKills(all, "github", "github", "wisp_github_a", true)); got != "bcd" {
		t.Errorf("everywhere: got %q, want everything but the current a", got)
	}
}
