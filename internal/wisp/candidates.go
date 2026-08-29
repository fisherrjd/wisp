package wisp

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
	byKey := map[string]Item{}
	var order []string

	for _, session := range LiveSessions() {
		name := ItemFor(session)
		if name == "" {
			name = session
		}
		state := StateLive
		if NeedsInput(session) {
			state = StateNeedsInput
		}
		Merge(byKey, &order, Item{Name: name, State: state})
	}

	local, err := c.LocalItems()
	if err != nil {
		return nil, err
	}
	for _, it := range local {
		Merge(byKey, &order, it)
	}

	remote, gitlabErr := c.GitLabItems()
	for _, it := range remote {
		Merge(byKey, &order, it)
	}

	out := make([]Item, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out, gitlabErr
}
