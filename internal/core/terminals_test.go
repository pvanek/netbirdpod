package core

import (
	"reflect"
	"strings"
	"testing"
)

// TestTerminalCommands checks the tab and split command lines of every
// terminal that has them, detected by environment or parent process.
func TestTerminalCommands(t *testing.T) {
	ssh := "ssh 100.64.0.9"
	tests := []struct {
		name    string
		env     map[string]string
		parents []string
		goos    string
		mode    Mode
		want    []string
		wait    bool // run (waited) rather than started
	}{
		{name: "tmux tab", env: map[string]string{"TMUX": "x", "GNOME_TERMINAL_SCREEN": "y"}, mode: ModeTab,
			want: []string{"tmux", "new-window", "-n", "web", ssh}, wait: true},
		{name: "zellij split", env: map[string]string{"ZELLIJ": "0"}, mode: ModeSplitRight,
			want: []string{"zellij", "run", "--direction", "right", "--", "ssh", "100.64.0.9"}, wait: true},
		{name: "gnome-terminal tab", env: map[string]string{"GNOME_TERMINAL_SCREEN": "/org/gnome/Terminal/screen/1"}, mode: ModeTab,
			want: []string{"gnome-terminal", "--tab", "--", "ssh", "100.64.0.9"}},
		{name: "gnome-terminal by process", parents: []string{"bash", "gnome-terminal-"}, mode: ModeTab,
			want: []string{"gnome-terminal", "--tab", "--", "ssh", "100.64.0.9"}},
		{name: "ptyxis tab", env: map[string]string{"PTYXIS_VERSION": "47.0"}, mode: ModeTab,
			want: []string{"ptyxis", "--tab", "--", "ssh", "100.64.0.9"}},
		{name: "console tab", env: map[string]string{"TERM_PROGRAM": "kgx"}, mode: ModeTab,
			want: []string{"kgx", "--tab", "--", "ssh", "100.64.0.9"}},
		{name: "xfce tab", parents: []string{"zsh", "xfce4-terminal"}, mode: ModeTab,
			want: []string{"xfce4-terminal", "--tab", "-x", "ssh", "100.64.0.9"}},
		{name: "mate tab", parents: []string{"bash", "mate-terminal"}, mode: ModeTab,
			want: []string{"mate-terminal", "--tab", "-x", "ssh", "100.64.0.9"}},
		{name: "tilix split right", env: map[string]string{"TILIX_ID": "abc"}, mode: ModeSplitRight,
			want: []string{"tilix", "--action=session-add-right", "-e", ssh}},
		{name: "tilix split down", env: map[string]string{"TILIX_ID": "abc"}, mode: ModeSplitDown,
			want: []string{"tilix", "--action=session-add-down", "-e", ssh}},
		{name: "terminator split right", env: map[string]string{"TERMINATOR_UUID": "urn:uuid:1"}, mode: ModeSplitRight,
			want: []string{"remotinator", "vsplit", "-x", ssh}},
		{name: "terminator split down", env: map[string]string{"TERMINATOR_UUID": "urn:uuid:1"}, mode: ModeSplitDown,
			want: []string{"remotinator", "hsplit", "-x", ssh}},
		{name: "kitty split", env: map[string]string{"KITTY_WINDOW_ID": "1", "KITTY_LISTEN_ON": "unix:/tmp/k"}, mode: ModeSplitRight,
			want: []string{"kitten", "@", "launch", "--title", "web", "--location=vsplit", "--", "ssh", "100.64.0.9"}, wait: true},
		{name: "wezterm split", env: map[string]string{"WEZTERM_PANE": "3"}, mode: ModeSplitDown,
			want: []string{"wezterm", "cli", "split-pane", "--bottom", "--", "ssh", "100.64.0.9"}, wait: true},
		{name: "elementary tab", parents: []string{"bash", "io.elementary.t"}, mode: ModeTab,
			want: []string{"io.elementary.terminal", "-t", "-x", "ssh", "100.64.0.9"}},
		{name: "iTerm2 split", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, goos: "darwin", mode: ModeSplitRight,
			want: []string{"osascript", "-e", `tell application "iTerm2" to tell current session of current window to split vertically with default profile command "ssh 100.64.0.9"`}, wait: true},
		{name: "Windows Terminal split", env: map[string]string{"WT_SESSION": "x"}, goos: "windows", mode: ModeSplitDown,
			want: []string{"wt", "-w", "0", "split-pane", "-H", "--title", "web", "ssh", "100.64.0.9"}},
		// The parent process wins over an inherited variable.
		{name: "konsole started from gnome-terminal", parents: []string{"bash", "xfce4-terminal"},
			env: map[string]string{"GNOME_TERMINAL_SCREEN": "y"}, mode: ModeTab,
			want: []string{"xfce4-terminal", "--tab", "-x", "ssh", "100.64.0.9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEnv{env: tt.env, parents: tt.parents, goos: tt.goos}
			res, err := f.launcher(LaunchOptions{}).SSH(peer, tt.mode)
			if err != nil || res.Note != "" {
				t.Fatalf("res %+v err %v", res, err)
			}
			got := f.started
			if tt.wait {
				got = f.ran
			}
			if len(got) != 1 || !reflect.DeepEqual(got[0], tt.want) {
				t.Fatalf("got started %q ran %q, want %q", f.started, f.ran, tt.want)
			}
		})
	}
}

func TestUnsupportedModesFallBack(t *testing.T) {
	// GNOME Terminal has tabs but no splits; Alacritty neither.
	f := &fakeEnv{env: map[string]string{"GNOME_TERMINAL_SCREEN": "y"}, installed: []string{"gnome-terminal"}}
	l := f.launcher(LaunchOptions{})
	if err := l.Available(ModeTab); err != nil {
		t.Fatalf("gnome tab: %v", err)
	}
	err := l.Available(ModeSplitRight)
	if err == nil || !strings.Contains(err.Error(), "GNOME Terminal cannot open a split") {
		t.Fatalf("gnome split: %v", err)
	}
	res, err := l.SSH(peer, ModeSplitRight)
	if err != nil || res.Where != "new GNOME Terminal window" || !strings.Contains(res.Note, "cannot open a split") {
		t.Fatalf("res %+v err %v", res, err)
	}
	if want := []string{"gnome-terminal", "--", "ssh", "100.64.0.9"}; !reflect.DeepEqual(f.started[0], want) {
		t.Fatalf("started %v", f.started)
	}

	f = &fakeEnv{env: map[string]string{"ALACRITTY_WINDOW_ID": "1"}, installed: []string{"alacritty"}}
	if err := f.launcher(LaunchOptions{}).Available(ModeTab); err == nil {
		t.Fatal("alacritty tab: want error")
	}
	// kitty without a remote control socket.
	f = &fakeEnv{env: map[string]string{"KITTY_WINDOW_ID": "1"}}
	if err := f.launcher(LaunchOptions{}).Available(ModeTab); err != errKittyLocked {
		t.Fatalf("kitty: %v", err)
	}
}

func TestWindowByDesktop(t *testing.T) {
	tests := []struct {
		desktop   string
		installed []string
		want      []string
	}{
		{"KDE", []string{"konsole", "xterm"}, []string{"konsole", "-e"}},
		{"GNOME", []string{"ptyxis", "gnome-terminal"}, []string{"ptyxis", "--new-window", "--"}},
		{"ubuntu:GNOME", []string{"gnome-terminal"}, []string{"gnome-terminal", "--"}},
		{"XFCE", []string{"xfce4-terminal"}, []string{"xfce4-terminal", "--window", "-x"}},
		{"MATE", []string{"mate-terminal"}, []string{"mate-terminal", "--window", "-x"}},
		{"X-Cinnamon", []string{"gnome-terminal"}, []string{"gnome-terminal", "--"}},
		{"LXQt", []string{"qterminal"}, []string{"qterminal", "-e"}},
		{"LXDE", []string{"lxterminal"}, []string{"lxterminal", "-e"}},
		{"Budgie:GNOME", []string{"tilix"}, []string{"tilix", "-e", "ssh 100.64.0.9"}}, // one string
		{"Pantheon", []string{"io.elementary.terminal"}, []string{"io.elementary.terminal", "-n", "-x"}},
		{"COSMIC", []string{"cosmic-term"}, []string{"cosmic-term", "--"}},
		{"Deepin", []string{"deepin-terminal"}, []string{"deepin-terminal", "-e"}},
		// Unknown desktop: x-terminal-emulator, then any known terminal.
		{"sway", []string{"x-terminal-emulator", "foot"}, []string{"x-terminal-emulator", "-e"}},
		{"sway", []string{"foot"}, []string{"foot"}},
	}
	for _, tt := range tests {
		f := &fakeEnv{env: map[string]string{"XDG_CURRENT_DESKTOP": tt.desktop}, installed: tt.installed}
		if _, err := f.launcher(LaunchOptions{}).SSH(peer, ModeWindow); err != nil {
			t.Fatalf("%s: %v", tt.desktop, err)
		}
		got := f.started[0]
		want := tt.want
		if !strings.HasPrefix(want[len(want)-1], "ssh") {
			want = append(want, "ssh", "100.64.0.9")
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s %v: got %q, want %q", tt.desktop, tt.installed, got, want)
		}
	}
}

func TestAppleString(t *testing.T) {
	if got := appleString(`ssh -o "a\b"`); got != `"ssh -o \"a\\b\""` {
		t.Fatalf("got %s", got)
	}
}
