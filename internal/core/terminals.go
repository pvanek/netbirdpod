package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A terminal is something that can open a command in a new window, and
// perhaps in a tab or split of the window netbirdpod runs in. The command
// lines come from each terminal's documentation and source; see the README
// for the table.

// cmdFunc builds a terminal command line around argv.
type cmdFunc func(argv []string, title string) []string

type terminal struct {
	name string
	bin  string   // binary for new windows
	comm []string // process names (/proc/<pid>/comm, cut to 15 bytes)
	// env reports whether the environment says we run inside it.
	env func(getenv func(string) string) bool

	window, tab, right, down cmdFunc
	// wait runs tab and split commands to completion to see their errors;
	// otherwise they start detached (clients that may become the terminal).
	wait bool
	// check is a precondition of tabs and splits, e.g. remote control.
	check func(l *Launcher) error
	// open replaces tab and split commands (Konsole's D-Bus).
	open func(l *Launcher, argv []string, title string, mode Mode) (string, error)
}

func (t *terminal) modeCmd(mode Mode) cmdFunc {
	switch mode {
	case ModeTab:
		return t.tab
	case ModeSplitRight:
		return t.right
	case ModeSplitDown:
		return t.down
	}
	return t.window
}

func envSet(names ...string) func(func(string) string) bool {
	return func(getenv func(string) string) bool {
		for _, n := range names {
			if getenv(n) != "" {
				return true
			}
		}
		return false
	}
}

func termProgram(value string) func(func(string) string) bool {
	return func(getenv func(string) string) bool { return getenv("TERM_PROGRAM") == value }
}

// prefixed returns a cmdFunc running argv after a fixed prefix.
func prefixed(prefix ...string) cmdFunc {
	return func(argv []string, _ string) []string {
		return append(append([]string{}, prefix...), argv...)
	}
}

// quoted returns a cmdFunc passing argv as one shell-quoted string.
func quoted(prefix ...string) cmdFunc {
	return func(argv []string, _ string) []string {
		return append(append([]string{}, prefix...), ShellJoin(argv))
	}
}

// Multiplexers: inside one, tabs and splits are its own windows and panes,
// whatever terminal shows it.
var multiplexers = []*terminal{
	{
		name: "tmux", env: envSet("TMUX"), wait: true,
		tab: func(argv []string, title string) []string {
			return []string{"tmux", "new-window", "-n", title, ShellJoin(argv)}
		},
		right: quoted("tmux", "split-window", "-h"),
		down:  quoted("tmux", "split-window", "-v"),
	},
	{
		name: "Zellij", env: envSet("ZELLIJ"), wait: true,
		right: prefixed("zellij", "run", "--direction", "right", "--"),
		down:  prefixed("zellij", "run", "--direction", "down", "--"),
	},
}

// terminals are the terminal emulators, roughly by popularity.
var terminals = []*terminal{
	{
		name: "Konsole", bin: "konsole", comm: []string{"konsole"},
		env:    envSet("KONSOLE_DBUS_SERVICE"),
		window: prefixed("konsole", "-e"),
		check:  func(l *Launcher) error { return l.konsoleCheck() },
		open: func(l *Launcher, argv []string, title string, mode Mode) (string, error) {
			return l.konsoleOpen(argv, title, mode)
		},
	},
	{
		name: "GNOME Terminal", bin: "gnome-terminal", comm: []string{"gnome-terminal-"},
		env:    envSet("GNOME_TERMINAL_SCREEN", "GNOME_TERMINAL_SERVICE"),
		window: prefixed("gnome-terminal", "--"),
		// Inside GNOME Terminal, --tab goes to the window of $GNOME_TERMINAL_SCREEN.
		tab: prefixed("gnome-terminal", "--tab", "--"),
	},
	{
		name: "Ptyxis", bin: "ptyxis", comm: []string{"ptyxis", "ptyxis-agent"},
		env:    envSet("PTYXIS_VERSION"),
		window: prefixed("ptyxis", "--new-window", "--"),
		tab:    prefixed("ptyxis", "--tab", "--"),
	},
	{
		name: "Console", bin: "kgx", comm: []string{"kgx"},
		env:    termProgram("kgx"),
		window: prefixed("kgx", "--"),
		tab:    prefixed("kgx", "--tab", "--"),
	},
	{
		name: "Xfce Terminal", bin: "xfce4-terminal", comm: []string{"xfce4-terminal"},
		window: prefixed("xfce4-terminal", "--window", "-x"),
		tab:    prefixed("xfce4-terminal", "--tab", "-x"),
	},
	{
		name: "MATE Terminal", bin: "mate-terminal", comm: []string{"mate-terminal"},
		window: prefixed("mate-terminal", "--window", "-x"),
		tab:    prefixed("mate-terminal", "--tab", "-x"),
	},
	{
		// Tilix parses -e like a shell command line; it must come last.
		name: "Tilix", bin: "tilix", comm: []string{"tilix"},
		env:    envSet("TILIX_ID"),
		window: quoted("tilix", "-e"),
		tab:    quoted("tilix", "--action=app-new-session", "-e"),
		right:  quoted("tilix", "--action=session-add-right", "-e"),
		down:   quoted("tilix", "--action=session-add-down", "-e"),
	},
	{
		// remotinator splits the pane of $TERMINATOR_UUID and runs -x with
		// $SHELL -c; terminator --new-tab goes to the first window.
		name: "Terminator", bin: "terminator", comm: []string{"terminator"},
		env:    envSet("TERMINATOR_UUID"),
		window: prefixed("terminator", "-x"),
		tab:    prefixed("terminator", "--new-tab", "-x"),
		right:  quoted("remotinator", "vsplit", "-x"),
		down:   quoted("remotinator", "hsplit", "-x"),
	},
	{
		// Needs allow_remote_control and listen_on: without a socket kitten
		// would talk over the tty netbirdpod reads. Splits follow the
		// "splits" layout; other layouts place the window themselves.
		name: "kitty", bin: "kitty", comm: []string{"kitty"},
		env:    envSet("KITTY_WINDOW_ID"),
		window: prefixed("kitty"),
		tab:    func(argv []string, title string) []string { return kittyLaunch(title, "--type=tab", argv) },
		right:  func(argv []string, title string) []string { return kittyLaunch(title, "--location=vsplit", argv) },
		down:   func(argv []string, title string) []string { return kittyLaunch(title, "--location=hsplit", argv) },
		wait:   true,
		check: func(l *Launcher) error {
			if l.getenv("KITTY_LISTEN_ON") == "" {
				return errKittyLocked
			}
			return nil
		},
	},
	{
		name: "WezTerm", bin: "wezterm", comm: []string{"wezterm-gui"},
		env:    envSet("WEZTERM_PANE"),
		window: prefixed("wezterm", "start", "--"),
		tab:    prefixed("wezterm", "cli", "spawn", "--"),
		right:  prefixed("wezterm", "cli", "split-pane", "--right", "--"),
		down:   prefixed("wezterm", "cli", "split-pane", "--bottom", "--"),
		wait:   true,
	},
	{
		name: "Alacritty", bin: "alacritty", comm: []string{"alacritty"},
		env:    envSet("ALACRITTY_WINDOW_ID"),
		window: prefixed("alacritty", "-e"),
	},
	{
		name: "Ghostty", bin: "ghostty", comm: []string{"ghostty"},
		env:    termProgram("ghostty"),
		window: prefixed("ghostty", "-e"),
	},
	{name: "foot", bin: "foot", comm: []string{"foot", "footclient"}, window: prefixed("foot")},
	{name: "QTerminal", bin: "qterminal", comm: []string{"qterminal"}, window: prefixed("qterminal", "-e")},
	{name: "LXTerminal", bin: "lxterminal", comm: []string{"lxterminal"}, window: prefixed("lxterminal", "-e")},
	{name: "COSMIC Terminal", bin: "cosmic-term", comm: []string{"cosmic-term"}, window: prefixed("cosmic-term", "--")},
	{name: "Deepin Terminal", bin: "deepin-terminal", comm: []string{"deepin-terminal"}, window: prefixed("deepin-terminal", "-e")},
	{
		name: "elementary Terminal", bin: "io.elementary.terminal", comm: []string{"io.elementary.t"},
		window: prefixed("io.elementary.terminal", "-n", "-x"),
		tab:    prefixed("io.elementary.terminal", "-t", "-x"),
	},
	{name: "xterm", bin: "xterm", comm: []string{"xterm"}, window: prefixed("xterm", "-e")},
}

func kittyLaunch(title, how string, argv []string) []string {
	return append([]string{"kitten", "@", "launch", "--title", title, how, "--"}, argv...)
}

// macOS and Windows terminals.
var (
	iterm2 = &terminal{
		name: "iTerm2", env: termProgram("iTerm.app"), wait: true,
		window: osascript(`tell application "iTerm2" to create window with default profile command %s`),
		tab:    osascript(`tell application "iTerm2" to tell current window to create tab with default profile command %s`),
		right:  osascript(`tell application "iTerm2" to tell current session of current window to split vertically with default profile command %s`),
		down:   osascript(`tell application "iTerm2" to tell current session of current window to split horizontally with default profile command %s`),
	}
	appleTerminal = &terminal{
		name: "Terminal", env: termProgram("Apple_Terminal"),
		window: func(argv []string, _ string) []string {
			return []string{"osascript", "-e", `tell application "Terminal" to do script ` + appleString(ShellJoin(argv)),
				"-e", `tell application "Terminal" to activate`}
		},
	}
	// Windows Terminal: -w 0 is the most recently used window; a literal ";"
	// separates wt commands.
	windowsTerminal = &terminal{
		name: "Windows Terminal", bin: "wt", env: envSet("WT_SESSION"),
		window: wtCmd("-w", "new", "new-tab"),
		tab:    wtCmd("-w", "0", "new-tab"),
		right:  wtCmd("-w", "0", "split-pane", "-V"),
		down:   wtCmd("-w", "0", "split-pane", "-H"),
	}
)

func osascript(format string) cmdFunc {
	return func(argv []string, _ string) []string {
		return []string{"osascript", "-e", fmt.Sprintf(format, appleString(ShellJoin(argv)))}
	}
}

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func wtCmd(prefix ...string) cmdFunc {
	return func(argv []string, title string) []string {
		out := append([]string{"wt"}, prefix...)
		out = append(out, "--title", title)
		for _, a := range argv {
			out = append(out, strings.ReplaceAll(a, ";", `\;`))
		}
		return out
	}
}

// deDefaults are the default terminals of desktop environments, by
// XDG_CURRENT_DESKTOP entry (lower case).
var deDefaults = map[string][]string{
	"kde":      {"konsole"},
	"gnome":    {"ptyxis", "gnome-terminal", "kgx"},
	"ubuntu":   {"ptyxis", "gnome-terminal"},
	"unity":    {"gnome-terminal"},
	"xfce":     {"xfce4-terminal"},
	"mate":     {"mate-terminal"},
	"cinnamon": {"gnome-terminal"},
	"lxqt":     {"qterminal"},
	"lxde":     {"lxterminal"},
	"budgie":   {"tilix", "gnome-terminal"},
	"pantheon": {"io.elementary.terminal"},
	"deepin":   {"deepin-terminal"},
	"dde":      {"deepin-terminal"},
	"cosmic":   {"cosmic-term"},
}

var (
	errNoTerminal  = errors.New("the terminal netbirdpod runs in is not one it can control")
	errKittyLocked = errors.New("kitty remote control is off (set allow_remote_control and listen_on in kitty.conf)")
)

// current is the multiplexer or terminal netbirdpod runs in, or nil.
// Multiplexers come first; then the parent processes, which are exact; then
// environment variables, which may be inherited from an outer terminal.
func (l *Launcher) current() *terminal {
	for _, t := range multiplexers {
		if t.env(l.getenv) {
			return t
		}
	}
	switch l.goos {
	case "darwin":
		for _, t := range []*terminal{iterm2, appleTerminal} {
			if t.env(l.getenv) {
				return t
			}
		}
		return nil
	case "windows":
		if windowsTerminal.env(l.getenv) {
			return windowsTerminal
		}
		return nil
	}
	for _, comm := range l.parents() {
		for _, t := range terminals {
			for _, c := range t.comm {
				if c == comm {
					return t
				}
			}
		}
	}
	for _, t := range terminals {
		if t.env != nil && t.env(l.getenv) {
			return t
		}
	}
	return nil
}

// procParents lists the process names of netbirdpod's ancestors, nearest
// first (Linux only; elsewhere none).
func procParents() []string {
	var out []string
	pid := os.Getppid()
	for range 32 {
		if pid <= 1 {
			break
		}
		comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
		if err != nil {
			break
		}
		out = append(out, strings.TrimSpace(string(comm)))
		stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			break
		}
		// The name in stat is in parentheses and may contain spaces.
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) < 2 {
			break
		}
		if pid, err = strconv.Atoi(fields[1]); err != nil {
			break
		}
	}
	return out
}

// Available reports why mode would fall back to a new window, or nil when
// it opens as asked. It asks the terminal without opening anything.
func (l *Launcher) Available(mode Mode) error {
	if mode == ModeHere || mode == ModeWindow {
		return nil
	}
	t := l.current()
	if t == nil {
		return errNoTerminal
	}
	if t.open == nil && t.modeCmd(mode) == nil {
		return fmt.Errorf("%s cannot open a %s from the command line", t.name, modeNoun(mode))
	}
	if t.check != nil {
		return t.check(l)
	}
	return nil
}

func modeNoun(mode Mode) string {
	if mode == ModeTab {
		return "tab"
	}
	return "split"
}

// openInTerminal opens a tab or split in the terminal netbirdpod runs in.
func (l *Launcher) openInTerminal(argv []string, title string, mode Mode) (string, error) {
	if err := l.Available(mode); err != nil {
		return "", err
	}
	t := l.current()
	if t.open != nil {
		return t.open(l, argv, title, mode)
	}
	cmd := t.modeCmd(mode)(argv, title)
	where := t.name + " " + modeNoun(mode)
	if mode == ModeTab {
		where = "new " + where
	}
	if t.wait {
		return where, l.run(cmd)
	}
	return where, l.start(cmd)
}

// openWindow opens a new terminal window running argv: the configured
// terminal, else the one netbirdpod runs in, $TERMINAL, the desktop's
// default, x-terminal-emulator, or any known terminal installed.
func (l *Launcher) openWindow(argv []string, title string) (string, error) {
	if len(l.opts.Terminal) > 0 {
		return "new " + l.opts.Terminal[0] + " window", l.start(append(append([]string{}, l.opts.Terminal...), argv...))
	}
	if t := l.current(); t != nil && t.window != nil && (t.bin == "" || l.installed(t.bin)) {
		return "new " + t.name + " window", l.start(t.window(argv, title))
	}
	switch l.goos {
	case "darwin":
		return "new Terminal window", l.start(appleTerminal.window(argv, title))
	case "windows":
		if l.installed("wt") {
			return "new Windows Terminal window", l.start(windowsTerminal.window(argv, title))
		}
		return "new console window", l.start(append([]string{"cmd", "/c", "start", title}, argv...))
	}
	if t := l.getenv("TERMINAL"); t != "" && l.installed(t) {
		return "new " + filepath.Base(t) + " window", l.start(append([]string{t, "-e"}, argv...))
	}
	for _, de := range strings.Split(strings.ToLower(l.getenv("XDG_CURRENT_DESKTOP")), ":") {
		de = strings.TrimPrefix(de, "x-")
		for _, bin := range deDefaults[de] {
			if t := terminalByBin(bin); t != nil && l.installed(bin) {
				return "new " + t.name + " window", l.start(t.window(argv, title))
			}
		}
	}
	if l.installed("x-terminal-emulator") {
		return "new terminal window", l.start(append([]string{"x-terminal-emulator", "-e"}, argv...))
	}
	for _, t := range terminals {
		if l.installed(t.bin) {
			return "new " + t.name + " window", l.start(t.window(argv, title))
		}
	}
	return "", errors.New("no terminal found for a new window; set \"terminal\" in the configuration")
}

func (l *Launcher) installed(bin string) bool {
	_, err := l.lookPath(bin)
	return err == nil
}

func terminalByBin(bin string) *terminal {
	for _, t := range terminals {
		if t.bin == bin {
			return t
		}
	}
	return nil
}
