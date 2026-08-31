package wisp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Version is wisp's own version. It lives here rather than in main because the two ends of a
// remote workspace are separate installs that have to be able to say what they are.
const Version = "0.11.0"

// WireVersion is the shape of what `wisp board --json` prints. The two ends are separate
// installs and will drift, so a mismatch refuses by name and number rather than half-parsing a
// shape it does not understand.
const WireVersion = 1

// BoardJSON is one workspace's state, as reported by the wisp that owns it.
type BoardJSON struct {
	Wire  int        `json:"wire"`
	Wisp  string     `json:"wisp"`
	Ready bool       `json:"ready"`
	Items []ItemJSON `json:"items"`
	Live  int        `json:"live"`
	Attn  int        `json:"attn"`
	// Note carries something worth saying that is not a failure, such as the GitLab source
	// being unconfigured. Reported rather than swallowed, for the same reason it is locally: an
	// off source and an empty one look identical in the picker.
	Note string `json:"note,omitempty"`
}

type ItemJSON struct {
	Name  string `json:"name"`
	State int    `json:"state"`
	Title string `json:"title,omitempty"`
}

// Items converts the wire form back into what the picker merges.
func (b BoardJSON) AsItems() []Item {
	out := make([]Item, 0, len(b.Items))
	for _, it := range b.Items {
		out = append(out, Item{Name: it.Name, State: State(it.State), Title: it.Title})
	}
	return out
}

// sshArgs are the options every call to a remote workspace carries.
//
// Interactive calls are the ones that hand a terminal over; everything else runs behind the
// picker, which has nowhere to show a password prompt and no way to accept the answer, so those
// must fail fast instead of blocking.
func (l Location) sshArgs(interactive bool) []string {
	args := []string{
		// The first call pays for the handshake and the rest reuse it. Without this a preview
		// per cursor move is unusable. %C is a hash rather than the hostname, which also keeps
		// the socket path inside the length a unix socket allows.
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=~/.ssh/wisp-%C",
		"-o", "ControlPersist=60s",
	}
	if interactive {
		return append(args, "-t", l.Host)
	}
	return append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=3", l.Host)
}

// command is the shell line run on the far side.
//
// The workspace is passed by path in the environment rather than by name with -w. The name is a
// local label: the other machine has its own config and no reason to know what you call its
// workspace here.
func (l Location) command(args ...string) string {
	return "WISP_WORKSPACE=" + remotePath(l.Path) + " " + bareCommand(args...)
}

// bareCommand is a wisp command on the far side that is not about a particular workspace, such
// as making one. Nothing is pinned, because there is nothing to pin yet.
func bareCommand(args ...string) string {
	var b strings.Builder
	b.WriteString("wisp")
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(a))
	}
	return b.String()
}

// remotePath quotes a path for the far side's shell while leaving a leading ~ for it to expand.
// Quoting the whole thing would hand wisp a literal tilde, which is not a directory anywhere.
func remotePath(p string) string {
	switch {
	case p == "~":
		return "~"
	case strings.HasPrefix(p, "~/"):
		return "~/" + shellQuote(strings.TrimPrefix(p, "~/"))
	default:
		return shellQuote(p)
	}
}

// SSHLine is the full ssh invocation as a shell string, for handing to tmux as a window command.
func (l Location) SSHLine(interactive bool, args ...string) string {
	parts := append([]string{"ssh"}, l.sshArgs(interactive)...)
	quoted := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		quoted = append(quoted, shellQuote(p))
	}
	return strings.Join(quoted, " ") + " " + shellQuote(l.command(args...))
}

// run executes a wisp command on the far side, against this workspace.
func (l Location) run(args ...string) ([]byte, error) { return l.exec(l.command(args...)) }

// runBare executes a wisp command on the far side that is not scoped to a workspace.
func (l Location) runBare(args ...string) ([]byte, error) { return l.exec(bareCommand(args...)) }

func (l Location) exec(line string) ([]byte, error) {
	if !l.IsRemote() {
		return nil, fmt.Errorf("not a remote workspace")
	}
	cmd := exec.Command("ssh", append(l.sshArgs(false), line)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, l.diagnose(err, stderr.String())
	}
	return out, nil
}

// diagnose turns an ssh exit status into something naming what to fix. The raw version is
// "exit status 255", which is true and useless.
func (l Location) diagnose(err error, stderr string) error {
	// The likeliest failure of all, and the most confusing without this: the far side is a wisp
	// from before remote workspaces existed, so it rejects the command and prints its own usage,
	// whose last line says nothing about why.
	if strings.Contains(stderr, `unknown command "board"`) {
		return fmt.Errorf("wisp on %s is too old for this: it has no board command (this one is %s)", l.Host, Version)
	}
	msg := lastLine(stderr)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 127:
			return fmt.Errorf("wisp is not on PATH on %s", l.Host)
		case 255:
			if msg == "" {
				msg = "ssh failed"
			}
			return fmt.Errorf("cannot reach %s: %s", l.Host, msg)
		}
	}
	if msg != "" {
		return fmt.Errorf("%s: %s", l.Host, msg)
	}
	return fmt.Errorf("%s: %w", l.Host, err)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// Board asks a remote workspace what it holds. With gitlab set it also merges that workspace's
// own remote source, which is the far side's business: it has the group, the token and the cache.
func (l Location) Board(gitlab bool) (BoardJSON, error) {
	args := []string{"board", "--json"}
	if gitlab {
		args = append(args, "--gitlab")
	}
	out, err := l.run(args...)
	if err != nil {
		return BoardJSON{}, err
	}
	var b BoardJSON
	if err := json.Unmarshal(out, &b); err != nil {
		return BoardJSON{}, fmt.Errorf("%s: unreadable board; wisp there is probably older than this one (%s)", l.Host, Version)
	}
	if b.Wire != WireVersion {
		return BoardJSON{}, fmt.Errorf("%s runs wisp %s speaking wire %d; this one speaks wire %d",
			l.Host, b.Wisp, b.Wire, WireVersion)
	}
	return b, nil
}

// Preview is the far side's preview text for an item, used when nothing is attached to it here.
func (l Location) Preview(item string, width int) (string, error) {
	out, err := l.run("preview", item, "--width", fmt.Sprint(width))
	return string(out), err
}

// probe is one remote workspace's board, or the error explaining why there is none.
type probe struct {
	board BoardJSON
	err   error
}

// probeRemotes fetches every remote workspace's board at once.
//
// In parallel because they are independent and each is a network round trip: doing them in turn
// would make the picker's load time the sum of every machine you have configured rather than the
// slowest one. Local workspaces cost nothing here and are skipped.
func (c Config) probeRemotes(gitlab bool) map[string]probe {
	type job struct {
		name string
		loc  Location
	}
	var jobs []job
	for name, loc := range c.Workspaces {
		if loc.IsRemote() {
			jobs = append(jobs, job{name, loc})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	out := make(map[string]probe, len(jobs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			b, err := j.loc.Board(gitlab)
			mu.Lock()
			out[j.name] = probe{board: b, err: err}
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	return out
}
