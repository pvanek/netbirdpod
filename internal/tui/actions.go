package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pvanek/netbirdpod/internal/core"
)

// Actions change the connection (up, down, login, switching profiles) or
// open something for a peer (ssh, RDP, nmap). Quick NetBird commands run in
// the background; "netbird up" and "login" may need an SSO login, so they
// run in the terminal with netbirdpod suspended, showing the login URL.

const actionTimeout = time.Minute

// actionMsg reports a finished action.
type actionMsg struct {
	label string // e.g. "netbird down"
	ok    string // message on success
	err   error
	// upNext connects this profile next (after switching to it).
	upNext string
	// noRefresh skips reloading the state afterwards (ssh, RDP).
	noRefresh bool
}

// handleActionKey handles the action keys of an item: a profile on the
// Connections tab, a peer on the Peers tab, or the item of a detail.
func (m *Model) handleActionKey(k string, item any) (tea.Cmd, bool) {
	if item == nil && m.tab == tabConnections {
		item = core.Profile{} // no profiles listed: act on the active one
	}
	switch it := item.(type) {
	case core.Profile:
		switch k {
		case "u":
			return m.up(it.Name), true
		case "D":
			m.askDown()
			return nil, true
		case "L":
			return m.inTerminal("netbird login", m.svc.LoginCommand(it.Name), "logged in"), true
		}
	case core.Peer:
		switch k {
		case "s":
			return m.openSSH(it, m.launch.SSHMode()), true
		case "S":
			m.openMenu(it)
			return nil, true
		case "d":
			res, err := m.launch.RDP(it)
			return m.afterLaunch("rdp "+it.Name(), res, err), true
		case "n", "N":
			return m.scan(it.IP, k == "N"), true
		}
	}
	if k == "c" && m.tab == tabPeers && m.detail == nil {
		m.connectedOnly = !m.connectedOnly
		m.table.SetCursor(0)
		return nil, true
	}
	return nil, false
}

// activeProfile is the profile NetBird uses now.
func (m Model) activeProfile() string {
	if m.status.loaded && m.status.data.Profile != "" {
		return m.status.data.Profile
	}
	p, _ := m.profiles.data.Active()
	return p.Name
}

// up connects profile, switching to it first when another one is active.
func (m *Model) up(profile string) tea.Cmd {
	if m.busy != "" {
		m.setFlash(flashWarn, m.busy+" is still running")
		return nil
	}
	active, st := m.activeProfile(), m.status.data
	if profile == "" || profile == active {
		if st.Connected() {
			m.setFlash(flashInfo, "already connected")
			return nil
		}
		return m.inTerminal("netbird up", m.svc.UpCommand(profile), "connected")
	}
	if !m.status.loaded || st.Err != nil || !st.Up() {
		return m.switchTo(profile, false)
	}
	m.confirm = &confirmView{
		prompt: fmt.Sprintf("Disconnect %q and connect %q?", active, profile),
		yes:    func(m *Model) tea.Cmd { return m.switchTo(profile, true) },
	}
	return nil
}

// switchTo makes profile the active one and then connects it.
func (m *Model) switchTo(profile string, down bool) tea.Cmd {
	m.busy = "switching to " + profile
	m.setFlash(flashInfo, "")
	svc := m.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		msg := actionMsg{label: "switch to " + profile}
		if down {
			if msg.err = svc.Down(ctx); msg.err != nil {
				return msg
			}
		}
		if msg.err = svc.SelectProfile(ctx, profile); msg.err == nil {
			msg.upNext = profile
		}
		return msg
	}
}

// askDown asks before disconnecting.
func (m *Model) askDown() {
	if m.busy != "" {
		m.setFlash(flashWarn, m.busy+" is still running")
		return
	}
	if m.status.loaded && m.status.data.Err == nil && !m.status.data.Up() {
		m.setFlash(flashInfo, "already disconnected")
		return
	}
	m.confirm = &confirmView{
		prompt: fmt.Sprintf("Disconnect %q from NetBird?", m.activeProfile()),
		yes:    func(m *Model) tea.Cmd { return m.down() },
	}
}

func (m *Model) down() tea.Cmd {
	m.busy = "netbird down"
	m.setFlash(flashInfo, "")
	svc := m.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionMsg{label: "netbird down", ok: "disconnected", err: svc.Down(ctx)}
	}
}

// onAction handles a finished action.
func (m *Model) onAction(msg actionMsg) tea.Cmd {
	m.busy = ""
	switch {
	case msg.err != nil:
		m.setFlash(flashErr, msg.label+": "+msg.err.Error())
	case msg.upNext != "":
		m.setFlash(flashInfo, "switched to "+msg.upNext)
		return tea.Batch(m.refreshAll(), m.inTerminal("netbird up", m.svc.UpCommand(msg.upNext), "connected "+msg.upNext))
	case msg.ok != "":
		m.setFlash(flashInfo, msg.ok)
	}
	if msg.noRefresh {
		return nil
	}
	return m.refreshAll()
}

// inTerminal runs cmd in the terminal with the UI suspended.
func (m *Model) inTerminal(label string, cmd *exec.Cmd, ok string) tea.Cmd {
	return m.runInTerminal(label, cmd, ok, false)
}

func (m *Model) runInTerminal(label string, cmd *exec.Cmd, ok string, noRefresh bool) tea.Cmd {
	tc := &terminalCmd{cmd: cmd, title: strings.Join(cmd.Args, " ")}
	return tea.Exec(tc, func(err error) tea.Msg {
		return actionMsg{label: label, ok: ok, err: err, noRefresh: noRefresh}
	})
}

// terminalCmd runs a command in the terminal and, if it fails, keeps its
// output on screen until enter is pressed.
type terminalCmd struct {
	cmd            *exec.Cmd
	title          string
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (c *terminalCmd) SetStdin(r io.Reader)  { c.stdin = r }
func (c *terminalCmd) SetStdout(w io.Writer) { c.stdout = w }
func (c *terminalCmd) SetStderr(w io.Writer) { c.stderr = w }

func (c *terminalCmd) Run() error {
	c.cmd.Stdin, c.cmd.Stdout, c.cmd.Stderr = c.stdin, c.stdout, c.stderr
	fmt.Fprintf(c.stdout, "\n\x1b[1m▶ %s\x1b[0m\n\n", c.title)
	err := c.cmd.Run()
	if err != nil {
		fmt.Fprintf(c.stdout, "\n\x1b[31m%s: %v\x1b[0m\nPress enter to return to netbirdpod…", c.title, err)
		if c.stdin != nil {
			_, _ = bufio.NewReader(c.stdin).ReadString('\n')
		}
	}
	return err
}

// --- ssh and RDP ---

func (m *Model) openSSH(p core.Peer, mode core.Mode) tea.Cmd {
	res, err := m.launch.SSH(p, mode)
	return m.afterLaunch("ssh "+p.Name(), res, err)
}

// afterLaunch reports where a session opened, or runs it in place.
func (m *Model) afterLaunch(label string, res core.Result, err error) tea.Cmd {
	switch {
	case err != nil:
		m.setFlash(flashErr, label+": "+err.Error())
		return nil
	case res.Inline != nil:
		return m.runInTerminal(label, res.Inline, "", true)
	case res.Note != "":
		m.setFlash(flashWarn, label+" opened in "+res.Where+" · "+res.Note)
	default:
		m.setFlash(flashInfo, label+" opened in "+res.Where)
	}
	return nil
}

// menuView chooses where ssh opens.
type menuView struct {
	peer core.Peer
	// fallback marks the modes that would open a new window instead.
	fallback map[core.Mode]bool
}

// openMenu asks the terminal which modes it can open, so the menu can
// mark those that fall back to a window, and says why.
func (m *Model) openMenu(p core.Peer) {
	menu := &menuView{peer: p, fallback: map[core.Mode]bool{}}
	var reasons []string
	for _, mm := range menuModes {
		err := m.launch.Available(mm.mode)
		if err == nil {
			continue
		}
		menu.fallback[mm.mode] = true
		if r := err.Error(); !slices.Contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}
	m.menu = menu
	if len(reasons) > 0 {
		m.setFlash(flashWarn, "tab and split open a new window: "+strings.Join(reasons, "; "))
	}
}

var menuModes = []struct {
	keys  string
	mode  core.Mode
	label string
}{
	{"1", core.ModeHere, "here"},
	{"2", core.ModeWindow, "window"},
	{"3", core.ModeTab, "tab"},
	{"4", core.ModeSplitRight, "split right"},
	{"5", core.ModeSplitDown, "split down"},
}

func (m Model) menuPrompt() string {
	parts := make([]string, len(menuModes))
	for i, mm := range menuModes {
		label := mm.label
		if m.menu.fallback[mm.mode] {
			label += "→window"
		}
		if mm.mode == m.launch.SSHMode() {
			label += "*"
		}
		parts[i] = mm.keys + " " + label
	}
	return "ssh " + m.menu.peer.Name() + " in: " + strings.Join(parts, " · ") + " · esc cancel"
}

func (m *Model) handleMenuKey(k string) tea.Cmd {
	p := m.menu.peer
	switch k {
	case "esc", "q", "S":
		m.menu = nil
		m.flash = flash{}
		return nil
	case "ctrl+c":
		return tea.Quit
	case "enter", "s":
		m.menu = nil
		return m.openSSH(p, m.launch.SSHMode())
	}
	for _, mm := range menuModes {
		if k == mm.keys {
			m.menu = nil
			return m.openSSH(p, mm.mode)
		}
	}
	return nil
}

// --- confirmation ---

type confirmView struct {
	prompt string
	yes    func(m *Model) tea.Cmd
}

func (m *Model) handleConfirmKey(k string) tea.Cmd {
	c := m.confirm
	switch k {
	case "y", "Y":
		m.confirm = nil
		return c.yes(m)
	case "ctrl+c":
		return tea.Quit
	case "n", "N", "esc", "q":
		m.confirm = nil
	}
	return nil
}
