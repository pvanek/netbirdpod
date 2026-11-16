package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Mode is where a command such as ssh opens.
type Mode string

const (
	ModeHere       Mode = "here"        // in this terminal; netbirdpod resumes when it exits
	ModeWindow     Mode = "window"      // a new terminal window
	ModeTab        Mode = "tab"         // a new tab of the terminal netbirdpod runs in
	ModeSplitRight Mode = "split-right" // a pane next to netbirdpod
	ModeSplitDown  Mode = "split-down"  // a pane below netbirdpod
)

// Modes lists every mode in menu order.
var Modes = []Mode{ModeHere, ModeWindow, ModeTab, ModeSplitRight, ModeSplitDown}

// ParseMode validates a configured mode name.
func ParseMode(s string) (Mode, error) {
	for _, m := range Modes {
		if string(m) == s {
			return m, nil
		}
	}
	names := make([]string, len(Modes))
	for i, m := range Modes {
		names[i] = string(m)
	}
	return "", fmt.Errorf("unknown mode %q (want one of %s)", s, strings.Join(names, ", "))
}

// PeerSettings are per-peer overrides, matched by name, FQDN or IP.
type PeerSettings struct {
	SSHUser string
	RDPUser string
	Host    string // address to connect to instead of the NetBird IP
}

// LaunchOptions configures how ssh and RDP sessions open.
type LaunchOptions struct {
	SSHCommand []string // default ["ssh"]; the [user@]host is appended
	SSHUser    string
	SSHMode    Mode // default ModeTab
	// UseFQDN connects to the peer's NetBird FQDN instead of its IP, which
	// needs NetBird DNS.
	UseFQDN bool
	Peers   map[string]PeerSettings
	// Terminal opens a new window running the command appended to it,
	// e.g. ["konsole", "-e"]; detected when empty.
	Terminal []string
	// RDPCommand is a template; {host}, {user} and {name} are replaced, and
	// arguments with {user} are dropped when no user is set. Detected when
	// empty.
	RDPCommand []string
	RDPUser    string
	// RDPMode is where terminal RDP clients (FreeRDP, which may prompt for a
	// password) open; default ModeWindow. GUI clients always start on their own.
	RDPMode Mode
}

// Result tells how a launch went.
type Result struct {
	// Inline is a command to run in netbirdpod's own terminal (ModeHere).
	Inline *exec.Cmd
	// Note explains a fallback, e.g. a window opened instead of a tab.
	Note string
	// Where describes where it opened, e.g. "new Konsole tab".
	Where string
}

// Launcher opens ssh and RDP sessions to peers.
type Launcher struct {
	opts     LaunchOptions
	getenv   func(string) string
	lookPath func(string) (string, error)
	goos     string
	// start starts a detached program; run runs one to completion.
	start func(argv []string) error
	run   func(argv []string) error
	// konsole connects to Konsole's D-Bus interface.
	konsole func(service string) (KonsoleBus, error)
	// parents lists the names of netbirdpod's parent processes.
	parents func() []string
}

func NewLauncher(opts LaunchOptions) *Launcher {
	if len(opts.SSHCommand) == 0 {
		opts.SSHCommand = []string{"ssh"}
	}
	if opts.SSHMode == "" {
		opts.SSHMode = ModeTab
	}
	if opts.RDPMode == "" {
		opts.RDPMode = ModeWindow
	}
	return &Launcher{
		opts:     opts,
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		goos:     runtime.GOOS,
		start:    startDetached,
		run: func(argv []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := runTool(ctx, argv[0], argv[1:]...)
			return err
		},
		konsole: dialKonsole,
		parents: procParents,
	}
}

// SSHMode is the configured default mode of ssh.
func (l *Launcher) SSHMode() Mode { return l.opts.SSHMode }

func startDetached(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap
	return nil
}

func (l *Launcher) settings(p Peer) PeerSettings {
	for _, k := range []string{p.Name(), p.FQDN, p.IP} {
		if s, ok := l.opts.Peers[k]; ok && k != "" {
			return s
		}
	}
	return PeerSettings{}
}

// Host is the address used to reach the peer.
func (l *Launcher) Host(p Peer) string {
	if h := l.settings(p).Host; h != "" {
		return h
	}
	if l.opts.UseFQDN && p.FQDN != "" {
		return p.FQDN
	}
	if p.IP != "" {
		return p.IP
	}
	return p.FQDN
}

// SSHUser is the user ssh logs in as; empty leaves it to ssh.
func (l *Launcher) SSHUser(p Peer) string {
	if u := l.settings(p).SSHUser; u != "" {
		return u
	}
	return l.opts.SSHUser
}

// RDPUser is the user the RDP client logs in as.
func (l *Launcher) RDPUser(p Peer) string {
	if u := l.settings(p).RDPUser; u != "" {
		return u
	}
	return l.opts.RDPUser
}

// SSHArgv is the ssh command line for the peer.
func (l *Launcher) SSHArgv(p Peer) []string {
	target := l.Host(p)
	if u := l.SSHUser(p); u != "" {
		target = u + "@" + target
	}
	return append(append([]string{}, l.opts.SSHCommand...), target)
}

// SSH opens an ssh session to the peer.
func (l *Launcher) SSH(p Peer, mode Mode) (Result, error) {
	return l.Open(l.SSHArgv(p), p.Name(), mode)
}

// Open runs argv in a new window, tab or pane, or returns it to run in
// place. Tabs and splits need a terminal netbirdpod can control (see
// terminals.go); otherwise they fall back to a new window.
func (l *Launcher) Open(argv []string, title string, mode Mode) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("empty command")
	}
	if mode == ModeHere {
		return Result{Inline: exec.Command(argv[0], argv[1:]...), Where: "this terminal"}, nil
	}
	var note string
	if mode != ModeWindow {
		where, err := l.openInTerminal(argv, title, mode)
		if err == nil {
			return Result{Where: where}, nil
		}
		note = fmt.Sprintf("%s: %v; opened a window instead", mode, err)
	}
	where, err := l.openWindow(argv, title)
	if err != nil {
		if note != "" {
			err = fmt.Errorf("%s; %w", strings.TrimSuffix(note, "; opened a window instead"), err)
		}
		return Result{}, err
	}
	return Result{Where: where, Note: note}, nil
}

// ShellJoin quotes argv for a POSIX shell.
func ShellJoin(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = shellQuote(a)
	}
	return strings.Join(out, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./-_", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- RDP ---

// rdpClient is a detected RDP client: its argument template and whether
// it needs a terminal (FreeRDP prompts there for passwords and certificates).
type rdpClient struct {
	argv     []string
	terminal bool
}

func (l *Launcher) rdpClient() (rdpClient, error) {
	if len(l.opts.RDPCommand) > 0 {
		return rdpClient{argv: l.opts.RDPCommand}, nil
	}
	switch l.goos {
	case "darwin":
		return rdpClient{argv: []string{"open", "rdp://full%20address=s:{host}"}}, nil
	case "windows":
		return rdpClient{argv: []string{"mstsc", "/v:{host}"}}, nil
	}
	candidates := []rdpClient{
		{argv: []string{"krdc", "rdp://{user}@{host}"}},
		{argv: []string{"remmina", "-c", "rdp://{user}@{host}"}},
	}
	for _, bin := range []string{"xfreerdp3", "sdl-freerdp3", "xfreerdp", "sdl-freerdp", "wlfreerdp"} {
		candidates = append(candidates, rdpClient{
			argv:     []string{bin, "/v:{host}", "/u:{user}", "/dynamic-resolution", "/cert:tofu"},
			terminal: true,
		})
	}
	for _, c := range candidates {
		if _, err := l.lookPath(c.argv[0]); err == nil {
			return c, nil
		}
	}
	return rdpClient{}, errors.New("no RDP client found: install krdc, remmina or FreeRDP, or set \"rdp_command\"")
}

// expand fills a command template. Arguments mentioning {user} are
// dropped without a user, except in URLs where only "{user}@" goes.
func expand(tmpl []string, host, user, name string) []string {
	var out []string
	for _, a := range tmpl {
		if strings.Contains(a, "{user}") && user == "" {
			if !strings.Contains(a, "{user}@") {
				continue
			}
			a = strings.ReplaceAll(a, "{user}@", "")
		}
		a = strings.NewReplacer("{host}", host, "{user}", user, "{name}", name).Replace(a)
		out = append(out, a)
	}
	return out
}

// RDPArgv is the RDP command line for the peer and whether it runs in a
// terminal.
func (l *Launcher) RDPArgv(p Peer) ([]string, bool, error) {
	c, err := l.rdpClient()
	if err != nil {
		return nil, false, err
	}
	return expand(c.argv, l.Host(p), l.RDPUser(p), p.Name()), c.terminal, nil
}

// RDP opens a remote desktop session to the peer.
func (l *Launcher) RDP(p Peer) (Result, error) {
	argv, terminal, err := l.RDPArgv(p)
	if err != nil {
		return Result{}, err
	}
	if terminal {
		return l.Open(argv, "rdp "+p.Name(), l.opts.RDPMode)
	}
	if err := l.start(argv); err != nil {
		return Result{}, err
	}
	return Result{Where: argv[0]}, nil
}
