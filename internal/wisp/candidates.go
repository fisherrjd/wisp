package wisp

import "sync"

// Candidates merges the three sources into the picker's list.
//
// Order is the whole design: live sessions first, then local vault folders, then GitLab. Merge
// keeps the first name it sees for a given identity and the highest state, so a hand-chosen
// local slug always beats the one derived from a GitLab title, and an item never appears twice
// under two spellings.
//
// GitLab errors are returned alongside the results rather than instead of them. The remote
// source is optional and a network failure must not empty the picker, but it should not be
// silent either, which is what the bash version did.
func (c Config) Candidates() ([]Item, error) {
	local, err := c.LocalCandidates()
	if err != nil {
		return nil, err
	}
	remote, gitlabErr := c.GitLabItems()
	return MergeAll(local, remote), gitlabErr
}

// LocalCandidates is everything reachable without the network: live tmux sessions and vault
// folders. It is what the picker paints first.
//
// Split out because the remote source can take most of a second on a cold cache, and blocking
// the whole list on it made the picker feel slow to open when the part that matters most, the
// sessions already running, was available immediately.
func (c Config) LocalCandidates() ([]Item, error) {
	byKey := map[string]Item{}
	var order []string

	// The needs-input check is a capture-pane per session, so run them together rather than
	// paying for each in turn.
	sessions := LiveSessions()
	states := make([]State, len(sessions))
	names := make([]string, len(sessions))
	var wg sync.WaitGroup
	for i, session := range sessions {
		wg.Add(1)
		go func(i int, session string) {
			defer wg.Done()
			names[i] = ItemFor(session)
			if names[i] == "" {
				names[i] = session
			}
			states[i] = StateLive
			if NeedsInput(session) {
				states[i] = StateNeedsInput
			}
		}(i, session)
	}
	wg.Wait()
	for i := range sessions {
		Merge(byKey, &order, Item{Name: names[i], State: states[i]})
	}

	local, err := c.LocalItems()
	if err != nil {
		return nil, err
	}
	for _, it := range local {
		Merge(byKey, &order, it)
	}

	out := make([]Item, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out, nil
}

// MergeAll folds later lists into the first, keeping its ordering and precedence. Used to add
// remote items to an already-painted local list.
func MergeAll(lists ...[]Item) []Item {
	byKey := map[string]Item{}
	var order []string
	for _, list := range lists {
		for _, it := range list {
			Merge(byKey, &order, it)
		}
	}
	out := make([]Item, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}
