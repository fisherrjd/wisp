package wisp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Closing an item out is a fifth thing an item can be, but not a fifth State.
//
// The four states are a ladder and Merge keeps the highest rung, so a done item found again as a
// live session or a still-open GitLab row would have the flag overwritten by whichever source
// spoke last. Done is a separate axis: it says what you decided, not what the machine observed,
// and only the two together decide whether a row is worth showing.
//
// The flag lives in notes.md rather than orchestration.md because every item has notes and only
// some have orchestration: an _adhoc item has no repos, so marking one finished would otherwise
// mean writing a manifest that describes nothing. Frontmatter in a note is also what a markdown
// vault's editor already understands, so this shows up there as a property rather than as stray
// text in the body.

// notesFrontmatter is the part of an item's note that wisp reads. Everything else in there is
// the human's and is never interpreted.
type notesFrontmatter struct {
	Done bool `yaml:"done"`
}

// NotesPath is where an item's notes live. One item, one note, created with the folder.
func (c Config) NotesPath(item string) string {
	return filepath.Join(c.ItemDir(item), "notes.md")
}

// ItemDone reports whether an item has been closed out.
//
// A missing, unreadable or malformed note is not done. Being unable to tell has to fall towards
// showing the row: a closed-out item that reappears is a small annoyance, and work that silently
// vanishes from the only list of it is not.
func (c Config) ItemDone(item string) bool {
	raw, err := os.ReadFile(c.NotesPath(item))
	if err != nil {
		return false
	}
	return doneIn(raw)
}

func doneIn(raw []byte) bool {
	fm, err := extractFrontmatter(raw)
	if err != nil || len(fm) == 0 {
		return false
	}
	var parsed notesFrontmatter
	if yaml.Unmarshal(fm, &parsed) != nil {
		return false
	}
	return parsed.Done
}

// SetDone marks an item closed out, or reopens it. Nothing on disk is removed either way: the
// note is the writing, and a close that destroys it is one nobody would trust enough to use.
func (c Config) SetDone(item string, done bool) error { return c.CloseOut(item, done, "") }

// CloseOut sets the flag and, given one, writes the line saying what finished.
//
// The two happen together because separately they did not happen at all. The flag was one
// keystroke and the write-up was a trip to an editor, so the items that got closed out and the
// items that got written up turned out to be disjoint sets: every item carrying the flag had a
// note holding nothing but the flag, while the one item with a real closing note was never
// marked. Whichever half you reach for now does the other.
func (c Config) CloseOut(item string, done bool, note string) error {
	path := c.NotesPath(item)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s has no notes.md to mark", item)
		}
		return err
	}
	next := raw
	if strings.TrimSpace(note) != "" {
		next = appendClosingNote(next, note, time.Now().Format("2006-01-02"))
	}
	next, err = setDoneIn(next, done)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if bytes.Equal(next, raw) {
		return nil
	}
	// Through a temp file in the same directory, so an interrupted write cannot leave the note
	// truncated. This is the one file in an item nobody else can reconstruct.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, next, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NoteIsEmpty reports whether an item's note says nothing yet.
//
// It is what decides whether closing out asks for a line first. A missing note counts as empty,
// which is the useful answer: there is nothing written down either way.
func (c Config) NoteIsEmpty(item string) bool {
	raw, err := os.ReadFile(c.NotesPath(item))
	if err != nil {
		return true
	}
	return !hasBody(raw)
}

// hasBody reports whether anything below the frontmatter was written by a person.
//
// The stub wisp creates with the folder is a single heading naming the item, so that one line
// does not count. Everything else does, including a heading someone added themselves: the test
// is "has anyone said anything here", not "is this a closing note".
func hasBody(raw []byte) bool {
	_, body, _ := splitFrontmatter(raw)
	stub := true
	for _, line := range bytes.Split(body, []byte("\n")) {
		t := bytes.TrimSpace(line)
		if len(t) == 0 {
			continue
		}
		if stub && bytes.HasPrefix(t, []byte("# ")) {
			stub = false
			continue
		}
		return true
	}
	return false
}

// appendClosingNote puts the line at the end of the body, under a dated heading.
//
// Dated because a note accumulates: an item worked over three weeks and closed out twice should
// read as two entries rather than as one paragraph that grew. The frontmatter is split off and
// put back untouched, so this composes with setDoneIn rather than fighting it.
func appendClosingNote(raw []byte, note, on string) []byte {
	fm, body, found := splitFrontmatter(raw)
	var b bytes.Buffer
	if found {
		b.WriteString("---\n")
		b.Write(fm)
		b.WriteString("\n---\n")
	}
	if trimmed := bytes.TrimRight(body, " \t\n"); len(trimmed) > 0 {
		b.Write(trimmed)
		b.WriteString("\n")
	}
	b.WriteString("\n## Closed out " + on + "\n\n")
	b.WriteString(strings.TrimSpace(note))
	b.WriteString("\n")
	return b.Bytes()
}

// setDoneIn rewrites a note's frontmatter to carry the flag, leaving the rest of the file alone.
//
// Edited as a YAML node rather than parsed into notesFrontmatter and re-emitted. The frontmatter
// belongs to whoever writes the note and can hold tags, aliases and anything else their editor
// puts there; a round trip through a one-field struct would silently delete all of it.
func setDoneIn(raw []byte, done bool) ([]byte, error) {
	fm, body, found := splitFrontmatter(raw)
	if !found {
		if !done {
			return raw, nil // no frontmatter and nothing to record is already the answer
		}
		return append([]byte("---\ndone: true\n---\n\n"), raw...), nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, err
	}
	// An empty frontmatter block parses to no document at all, so there is no mapping to edit
	// and one has to be started.
	var mapping *yaml.Node
	if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		mapping = doc.Content[0]
	} else if len(doc.Content) == 0 {
		mapping = &yaml.Node{Kind: yaml.MappingNode}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapping}}
	} else {
		return nil, fmt.Errorf("frontmatter is not a mapping")
	}

	value := "false"
	if done {
		value = "true"
	}
	set := false
	// Keys and values alternate in a mapping's content, so the value is always the next node.
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "done" {
			mapping.Content[i+1].Kind = yaml.ScalarNode
			mapping.Content[i+1].Tag = "!!bool"
			mapping.Content[i+1].Value = value
			set = true
			break
		}
	}
	if !set {
		if !done {
			return raw, nil // reopening something that was never closed changes nothing
		}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "done"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value})
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(out)
	b.WriteString("---\n")
	b.Write(body)
	return b.Bytes(), nil
}

// splitFrontmatter separates a note's frontmatter from everything after the closing fence. The
// body is returned verbatim, including its leading newline, so reassembly cannot shift it.
func splitFrontmatter(raw []byte) (fm, body []byte, found bool) {
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), fence) {
		return nil, raw, false
	}
	for i := 1; i < len(lines); i++ {
		if bytes.Equal(bytes.TrimSpace(lines[i]), fence) {
			return bytes.Join(lines[1:i], []byte("\n")), bytes.Join(lines[i+1:], []byte("\n")), true
		}
	}
	return nil, raw, false // opened but never closed; left for extractFrontmatter to report
}

// HideDone drops closed-out items from a list on its way to being shown.
//
// A live session keeps its row regardless. Something is running under that name, and a picker
// that hid it would be lying about what is on the machine: the session would still be there,
// still holding an agent, with nothing left in the list pointing at it.
//
// Applied at the points where a list is displayed rather than inside Items, which stays the
// truthful answer. Filtering at the source would drop the row before the GitLab pass merged
// over it, and a still-open ticket would then re-add the item that had just been hidden.
func HideDone(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Done && it.State < StateLive {
			continue
		}
		out = append(out, it)
	}
	return out
}

// DoneOnly is the complement, for looking back over what has been closed out.
func DoneOnly(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Done {
			out = append(out, it)
		}
	}
	return out
}
