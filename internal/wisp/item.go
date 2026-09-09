package wisp

import (
	"regexp"
	"strings"
)

// State is what the picker's glyph column encodes. Order matters: when the same item is found
// by more than one source, the highest state wins, so a live session always beats the vault
// folder it came from and the vault folder beats the GitLab item.
type State int

const (
	StateRemote     State = iota // on GitLab, nothing local
	StateFolder                  // vault folder exists, no session
	StateLive                    // tmux session running
	StateNeedsInput              // running, but the agent is waiting on a human
)

func (s State) Glyph() string {
	switch s {
	case StateNeedsInput:
		return "?"
	case StateLive:
		return "●"
	case StateFolder:
		return "○"
	default:
		return "+"
	}
}

func (s State) Label() string {
	switch s {
	case StateNeedsInput:
		return "needs input"
	case StateLive:
		return "live"
	case StateFolder:
		return "folder"
	default:
		return "gitlab"
	}
}

// Item is a unit of work: a vault folder named <repo>/<iid>-<slug>, or _adhoc/<name> for work
// with no ticket behind it.
type Item struct {
	Name  string
	State State
	Title string // GitLab title, when the item came from there
	// Done is set from the vault folder's notes.md. Not a State, because the states are a ladder
	// and this is a separate axis: an item can be finished and still have a session running on
	// it, and the two facts do not overrule each other. See done.go.
	Done bool
}

var iidRe = regexp.MustCompile(`^([^/]+)/([0-9]+)-`)

// Key is the stable identity, <repo>/<iid>. Slugs are hand-chosen locally and derived from the
// title on GitLab, so the same item routinely appears under two spellings and only this
// deduplicates correctly. Items with no iid (_adhoc, or a folder that is just a name) are their
// own key.
func (i Item) Key() string {
	if m := iidRe.FindStringSubmatch(i.Name); m != nil {
		return m[1] + "/" + m[2]
	}
	return i.Name
}

// IID is the ticket number in the name, or "" for an item that has none.
func (i Item) IID() string {
	if m := iidRe.FindStringSubmatch(i.Name); m != nil {
		return m[2]
	}
	return ""
}

// Repo is the directory the item belongs to, or "" for _adhoc items, which have no repo.
func (i Item) Repo() string {
	repo, _, ok := strings.Cut(i.Name, "/")
	if !ok || repo == "_adhoc" {
		return ""
	}
	return repo
}

// Slug is the trailing path element, used to name worktrees and branches.
func (i Item) Slug() string {
	if idx := strings.LastIndex(i.Name, "/"); idx >= 0 {
		return i.Name[idx+1:]
	}
	return i.Name
}

// Merge folds a newly discovered item into a set keyed by identity, keeping the higher state
// and preferring the name that is already there. That name preference is what makes a
// hand-chosen local slug win over the one derived from a GitLab title.
func Merge(into map[string]Item, order *[]string, it Item) {
	k := it.Key()
	existing, seen := into[k]
	if !seen {
		into[k] = it
		*order = append(*order, k)
		return
	}
	if it.State > existing.State {
		existing.State = it.State
	}
	if existing.Title == "" {
		existing.Title = it.Title
	}
	// Kept if any source knew it, rather than taken from the winner. Only the vault row reads the
	// note, so a session or a GitLab row merging over it carries Done=false and would otherwise
	// reopen an item by arriving second.
	if it.Done {
		existing.Done = true
	}
	into[k] = existing
}
