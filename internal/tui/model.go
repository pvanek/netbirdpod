// Package tui is the Bubble Tea terminal front end over internal/core.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pvanek/netbirdpod/internal/core"
)

// Fetcher is the subset of core.Service the UI needs (fakeable in tests).
type Fetcher interface {
	Status(ctx context.Context, now time.Time) core.Status
	Profiles(ctx context.Context, now time.Time) core.ProfileList
	Scan(ctx context.Context, ip string, full bool, now time.Time) core.Scan
	ScanArgs(ip string, full bool) []string
	Down(ctx context.Context) error
	SelectProfile(ctx context.Context, name string) error
	UpCommand(profile string) *exec.Cmd
	LoginCommand(profile string) *exec.Cmd
}

// Launcher opens sessions to peers (core.Launcher, fakeable in tests).
type Launcher interface {
	SSH(p core.Peer, mode core.Mode) (core.Result, error)
	RDP(p core.Peer) (core.Result, error)
	SSHMode() core.Mode
	// Available says why a mode would open a window instead, or nil.
	Available(mode core.Mode) error
	SSHArgv(p core.Peer) []string
	RDPArgv(p core.Peer) ([]string, bool, error)
}

type tab int

const (
	tabConnections tab = iota
	tabThisPeer
	tabPeers
	tabCount
)

var tabNames = [tabCount]string{"Connections", "This peer", "Peers"}

type Model struct {
	svc     Fetcher
	launch  Launcher
	now     func() time.Time
	refresh time.Duration

	width, height int
	tab           tab

	status   resource[core.Status]
	profiles resource[core.ProfileList]
	// scans holds nmap results by peer IP. The map is shared between model
	// copies; Bubble Tea calls Update from one goroutine, so that is safe.
	scans map[string]*resource[core.Scan]

	auto          [tabCount]bool // auto-refresh per tab
	connectedOnly bool           // Peers tab filter

	table   table.Model
	items   []any          // item behind each table row
	info    viewport.Model // This peer tab
	detail  *detailView
	help    *helpView    // over everything else when open
	menu    *menuView    // ssh mode chooser
	confirm *confirmView // yes/no question
	spinner spinner.Model

	busy  string // running action, e.g. "netbird down"
	flash flash  // result of the last action
}

// flash is a one-line message about the last action.
type flash struct {
	text string
	kind flashKind
}

type flashKind int

const (
	flashInfo flashKind = iota
	flashWarn
	flashErr
)

func New(svc Fetcher, launch Launcher, refresh time.Duration) Model {
	t := table.New(table.WithFocused(true))
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).BorderStyle(lipgloss.NormalBorder()).BorderBottom(true)
	s.Selected = s.Selected.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("57"))
	t.SetStyles(s)

	m := Model{
		svc:     svc,
		launch:  launch,
		now:     time.Now,
		refresh: refresh,
		scans:   map[string]*resource[core.Scan]{},
		table:   t,
		info:    viewport.New(),
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	for i := range m.auto {
		m.auto[i] = true
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.ensureVisible(), autoTick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var follow tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		cmd := m.handleKeyPress(msg)
		m.rebuildTable() // a key may change the header (messages, filter)
		return m, cmd
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case loadedMsg[core.Status]:
		m.status = resource[core.Status]{loaded: true, data: msg.data}
	case loadedMsg[core.ProfileList]:
		m.profiles = resource[core.ProfileList]{loaded: true, data: msg.data}
	case scanMsg:
		m.scans[msg.data.IP] = &resource[core.Scan]{loaded: true, data: msg.data}
		if msg.data.Err != nil {
			m.setFlash(flashErr, "scan of "+msg.data.IP+": "+msg.data.Err.Error())
		} else {
			m.setFlash(flashInfo, fmt.Sprintf("scan of %s done: %d open ports", msg.data.IP, len(msg.data.Open())))
		}
	case actionMsg:
		follow = m.onAction(msg)
	case autoTickMsg:
		follow = tea.Batch(m.onAutoTick(), autoTick())
	default:
		return m, nil
	}
	m.rebuildTable()
	return m, follow
}

// handleKeyPress routes a key to the topmost open layer.
func (m *Model) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case m.help != nil:
		return m.handleHelpKey(msg)
	case m.confirm != nil:
		return m.handleConfirmKey(msg.String())
	case m.menu != nil:
		return m.handleMenuKey(msg.String())
	}
	// After any key, load what the (possibly new) screen shows.
	if m.detail != nil {
		cmd := m.handleDetailKey(msg)
		return tea.Batch(cmd, m.ensureVisible())
	}
	if cmd, handled := m.handleKey(msg.String()); handled {
		m.rebuildTable()
		return tea.Batch(cmd, m.ensureVisible())
	}
	var cmd tea.Cmd
	if m.tab == tabThisPeer {
		m.info, cmd = m.info.Update(msg)
	} else {
		m.table, cmd = m.table.Update(msg)
	}
	return cmd
}

func (m *Model) handleKey(k string) (tea.Cmd, bool) {
	switch k {
	case "q", "ctrl+c":
		return tea.Quit, true
	case "tab", "right", "l":
		m.setTab((m.tab + 1) % tabCount)
	case "shift+tab", "left", "h":
		m.setTab((m.tab + tabCount - 1) % tabCount)
	case "0", "1", "2":
		m.setTab(tab(k[0] - '0'))
	case "enter":
		m.openDetail()
	case "r":
		m.flash = flash{}
		return m.refreshVisible(), true
	case "a":
		return m.toggleAuto(), true
	case "f1", "?":
		m.openHelp()
	default:
		return m.handleActionKey(k, m.selected())
	}
	return nil, true
}

func (m *Model) setTab(t tab) {
	m.tab = t
	m.flash = flash{}
	m.table.SetCursor(0)
	m.info.GotoTop()
}

// selected is the item of the open detail, or else of the selected row.
func (m Model) selected() any {
	if m.detail != nil {
		return m.detail.item
	}
	if m.tab == tabThisPeer {
		return nil
	}
	if i := m.table.Cursor(); i >= 0 && i < len(m.items) {
		return m.items[i]
	}
	return nil
}

func (m *Model) setFlash(kind flashKind, text string) {
	m.flash = flash{text: text, kind: kind}
}

var (
	activeTab   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("57")).Padding(0, 1)
	inactiveTab = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	dim         = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warn        = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	good        = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	scopeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	promptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("130")).Padding(0, 1)
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "netbirdpod"
	return v
}

func (m Model) render() string {
	if m.help != nil {
		return m.renderHelp()
	}
	if m.detail != nil {
		return m.renderDetail()
	}
	body := m.table.View()
	if m.tab == tabThisPeer {
		body = m.info.View()
	}
	return m.header() + "\n" + body + "\n" + m.footer(m.tabHints())
}

func (m Model) tabHints() string {
	switch m.tab {
	case tabConnections:
		return "F1 help · u up · D down · L login · enter detail · r refresh"
	case tabThisPeer:
		return "F1 help · ←/→ tab · ↑/↓ scroll · r refresh"
	}
	return "F1 help · s ssh · S ssh in… · d rdp · n/N scan · c connected only · enter detail"
}

// footer is the status bar, or the open prompt in its place.
func (m Model) footer(hints string) string {
	switch {
	case m.confirm != nil:
		return promptStyle.Render(truncate(m.confirm.prompt+"  y/n", max(m.width-2, 10)))
	case m.menu != nil:
		return promptStyle.Render(truncate(m.menuPrompt(), max(m.width-2, 10)))
	}
	return m.statusBar(hints)
}

// statusBar renders the bottom line: the given key hints, the auto-refresh
// state and last update, and "q quit" at the right edge. Hints are cut
// first when the screen is narrow.
func (m Model) statusBar(hints string) string {
	const right = "q quit"
	width := max(m.width, 20)
	state := m.autoLabel()
	if t := m.lastFetch(); !t.IsZero() {
		if updated := state + " · updated " + t.Local().Format("15:04:05"); lipgloss.Width(updated)+len(right)+8 <= width {
			state = updated
		}
	}
	room := width - lipgloss.Width(state) - len(right) - 4 // " · " and a space before right
	left := state
	if room >= 4 {
		left = truncate(hints, room) + " · " + state
	}
	gap := max(width-lipgloss.Width(left)-len(right), 1)
	return dim.Render(left + strings.Repeat(" ", gap) + right)
}

// header is everything above the table: tabs, connection, notes, messages.
func (m Model) header() string {
	var b strings.Builder
	b.WriteString(m.tabsLine() + "\n" + m.connLine())
	if m.tab == tabPeers {
		b.WriteString("\n" + m.peersNote())
	}
	if line := m.messageLine(); line != "" {
		b.WriteString("\n" + line)
	}
	return b.String()
}

// messageLine shows the last action's result, or else a load error.
func (m Model) messageLine() string {
	width := max(m.width, 20)
	if m.flash.text != "" {
		style := dim
		switch m.flash.kind {
		case flashWarn:
			style = warn
		case flashErr:
			style = errStyle
		}
		return style.Width(width).Render(m.flash.text)
	}
	if err := m.visibleErr(); err != nil {
		return errStyle.Width(width).Render("⚠ " + strings.ReplaceAll(err.Error(), "\n", " · "))
	}
	return ""
}

// tabsLine renders the tab bar; on narrow screens inactive tabs show only
// their number.
func (m Model) tabsLine() string {
	render := func(short bool) string {
		tabs := make([]string, tabCount)
		for i, name := range tabNames {
			style, label := inactiveTab, fmt.Sprintf("%d %s", i, name)
			if tab(i) == m.tab {
				style = activeTab
			} else if short {
				label = fmt.Sprint(i)
			}
			tabs[i] = style.Render(label)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	}
	if line := render(false); lipgloss.Width(line) <= m.width {
		return line
	}
	return render(true)
}

// connLine summarises the connection: profile, state, this peer.
func (m Model) connLine() string {
	parts := []string{}
	st := m.status
	switch {
	case !st.loaded:
		parts = append(parts, dim.Render("NetBird: ")+dim.Render("…"))
	case st.data.Err != nil:
		parts = append(parts, "NetBird: "+errStyle.Render("unavailable"))
	default:
		d := st.data
		parts = append(parts, "Profile: "+scopeStyle.Render(d.Profile), daemonStyled(d.Daemon))
		if d.FQDN != "" {
			parts = append(parts, d.FQDN+" "+dim.Render(stripMask(d.IP)))
		}
		if d.Connected() {
			parts = append(parts, fmt.Sprintf("peers %d/%d", d.PeersConnected, len(d.Peers)))
		}
	}
	line := strings.Join(parts, dim.Render(" · "))
	if label := m.loadingLabel(); label != "" {
		line += "  " + m.spinner.View() + dim.Render(" "+label)
	}
	return lipgloss.NewStyle().MaxWidth(max(m.width, 20)).Render(line)
}

// loadingLabel names what is running: an action, or a load.
func (m Model) loadingLabel() string {
	if m.busy != "" {
		return m.busy + "…"
	}
	if m.visibleLoading() {
		return "loading…"
	}
	for _, s := range m.scans {
		if s.loading {
			return "scanning…"
		}
	}
	return ""
}

// daemonStyled colours a daemon state.
func daemonStyled(s string) string {
	label := daemonLabel(s)
	switch s {
	case core.DaemonConnected:
		return good.Render(label)
	case core.DaemonConnecting:
		return warn.Render(label)
	case core.DaemonNeedsLogin, core.DaemonLoginFailed, core.DaemonSessionExpired:
		return errStyle.Render(label)
	}
	return dim.Render(label)
}

// daemonLabel marks a daemon state with a symbol, readable without colour.
func daemonLabel(s string) string {
	switch s {
	case core.DaemonConnected:
		return "● " + s
	case core.DaemonConnecting:
		return "◐ " + s
	case core.DaemonNeedsLogin, core.DaemonLoginFailed, core.DaemonSessionExpired:
		return "! " + s
	case "":
		return "–"
	case core.DaemonIdle:
		return "○ Disconnected"
	}
	return "○ " + s
}

// stripMask drops the prefix length of "10.0.0.1/16".
func stripMask(ip string) string {
	addr, _, _ := strings.Cut(ip, "/")
	return addr
}
