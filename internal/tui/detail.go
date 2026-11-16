package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pvanek/netbirdpod/internal/core"
)

const labelWidth = 18

var (
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Width(labelWidth)
)

// detailView is the full-screen detail of one table row.
type detailView struct {
	item any // core.Profile or core.Peer
	vp   viewport.Model
}

func (m *Model) openDetail() {
	it := m.selected()
	if it == nil {
		return
	}
	m.detail = &detailView{item: it, vp: viewport.New()}
	m.syncDetail()
}

// syncDetail re-resolves the shown item after a data refresh and re-renders
// the viewport for the current size. An item that disappeared keeps
// showing its last known state.
func (m *Model) syncDetail() {
	if m.detail == nil {
		return
	}
	key := itemKey(m.detail.item)
	for _, it := range m.allItems() {
		if itemKey(it) == key {
			m.detail.item = it
			break
		}
	}
	m.detail.vp.SetWidth(max(m.width, 20))
	m.detail.vp.SetHeight(max(m.height-lipgloss.Height(m.detailHead())-1, 3))
	m.detail.vp.SetContent(m.detailBody(m.detail.item))
}

// allItems lists every profile and peer, filtered out or not.
func (m Model) allItems() []any {
	var out []any
	for _, p := range m.profiles.data.Profiles {
		out = append(out, p)
	}
	for _, p := range m.status.data.Peers {
		out = append(out, p)
	}
	return out
}

// handleDetailKey handles keys while a detail is open. Keys that switch the
// tab close the detail and are then handled as usual.
func (m *Model) handleDetailKey(msg tea.KeyPressMsg) tea.Cmd {
	switch k := msg.String(); k {
	case "esc", "backspace", "left", "h":
		m.detail = nil
		m.rebuildTable()
		return nil
	case "f1", "?":
		m.openHelp()
		return nil
	case "q", "ctrl+c", "r", "a":
		cmd, _ := m.handleKey(k)
		return cmd
	case "tab", "shift+tab", "0", "1", "2":
		m.detail = nil
		cmd, _ := m.handleKey(k)
		m.rebuildTable()
		return cmd
	default:
		if cmd, handled := m.handleActionKey(k, m.detail.item); handled {
			m.syncDetail()
			return cmd
		}
	}
	var cmd tea.Cmd
	m.detail.vp, cmd = m.detail.vp.Update(msg)
	return cmd
}

func (m Model) detailHead() string {
	head := m.tabsLine() + "\n" + m.connLine() + "\n" +
		activeTab.Render(truncate(m.detailTitle(m.detail.item), max(m.width-2, 10)))
	if line := m.messageLine(); line != "" {
		head += "\n" + line
	}
	return head
}

func (m Model) renderDetail() string {
	hints := "F1 help · esc back · ↑/↓ scroll · r refresh"
	switch m.detail.item.(type) {
	case core.Peer:
		hints = "F1 help · esc back · s ssh · S ssh in… · d rdp · n/N scan"
	case core.Profile:
		hints = "F1 help · esc back · u up · D down · L login"
	}
	return strings.Join([]string{m.detailHead(), m.detail.vp.View(), m.footer(hints)}, "\n")
}

// itemKey identifies an item across refreshes.
func itemKey(it any) string {
	switch v := it.(type) {
	case core.Profile:
		return "profile/" + v.Name
	case core.Peer:
		return "peer/" + v.Key()
	}
	return ""
}

func (m Model) detailTitle(it any) string {
	switch v := it.(type) {
	case core.Profile:
		return "Profile " + v.Name
	case core.Peer:
		return "Peer " + v.Name()
	}
	return ""
}

// sheet accumulates labelled lines, wrapping values to the screen width.
type sheet struct {
	width int
	b     strings.Builder
}

func (s *sheet) section(title string) {
	if s.b.Len() > 0 {
		s.b.WriteString("\n")
	}
	s.b.WriteString(sectionStyle.Render(title) + "\n")
}

func (s *sheet) field(label, value string) {
	if value == "" {
		value = dim.Render("—")
	}
	wrapped := lipgloss.NewStyle().Width(max(s.width-labelWidth-1, 10)).Render(value)
	for i, line := range strings.Split(wrapped, "\n") {
		l := ""
		if i == 0 {
			l = label
		}
		s.b.WriteString(labelStyle.Render(l) + " " + line + "\n")
	}
}

// list writes one line per value under label, or "none".
func (s *sheet) list(label string, values []string) {
	if len(values) == 0 {
		s.field(label, dim.Render("none"))
		return
	}
	for i, v := range values {
		if i > 0 {
			label = ""
		}
		s.field(label, v)
	}
}

func (s *sheet) errField(label string, err error) {
	if err != nil {
		s.field(label, errStyle.Render(err.Error()))
	}
}

func (s *sheet) String() string { return strings.TrimRight(s.b.String(), "\n") }

func (m Model) detailBody(it any) string {
	s := &sheet{width: max(m.width, 30)}
	switch v := it.(type) {
	case core.Profile:
		m.profileDetail(s, v)
	case core.Peer:
		m.peerDetail(s, v)
	}
	return s.String()
}

// loadingText stands in for data that is loading or not loaded yet.
func loadingText(loading bool) string {
	if loading {
		return dim.Render("loading…")
	}
	return dim.Render("not loaded")
}

func (m Model) profileDetail(s *sheet, p core.Profile) {
	s.section("Profile")
	s.field("Name", p.Name)
	s.field("Active", yesNo(p.Active))
	s.field("Account", p.Email)
	if !p.Active {
		s.field("", dim.Render("NetBird reports the state of the active profile only; u connects this one (switching profiles)"))
		return
	}
	switch {
	case !m.status.loaded:
		s.field("Status", loadingText(m.status.loading))
	case m.status.data.Err != nil:
		s.errField("Error", m.status.data.Err)
	default:
		m.connectionSection(s, m.status.data)
	}
}

// connectionSection describes the connection of the active profile.
func (m Model) connectionSection(s *sheet, st core.Status) {
	now := m.now()
	s.section("Connection")
	s.field("Status", daemonStyled(st.Daemon))
	if st.NeedsLogin() {
		s.field("", warn.Render("press L to log in"))
	}
	s.field("Management", serviceText(st.Management))
	s.field("Signal", serviceText(st.Signal))
	expires := ""
	if !st.SessionExpiresAt.IsZero() {
		expires = fmtTime(st.SessionExpiresAt) + dim.Render(" ("+until(st.SessionExpiresAt, now)+")")
	}
	s.field("Session expires", expires)
	s.field("Peers", fmt.Sprintf("%d of %d connected", st.PeersConnected, len(st.Peers)))
}

func serviceText(e core.Endpoint) string {
	if e.URL == "" {
		return ""
	}
	state := good.Render("connected")
	if !e.Connected {
		state = errStyle.Render("disconnected")
	}
	out := e.URL + "  " + state
	if e.Error != "" {
		out += "  " + errStyle.Render(e.Error)
	}
	return out
}

// thisPeerBody is the content of the This peer tab.
func (m Model) thisPeerBody() string {
	s := &sheet{width: max(m.width, 30)}
	switch {
	case !m.status.loaded:
		s.field("Status", loadingText(m.status.loading))
		return s.String()
	case m.status.data.Err != nil:
		s.section("NetBird")
		s.errField("Error", m.status.data.Err)
		s.field("", dim.Render("is the NetBird daemon running? (netbird service status)"))
		return s.String()
	}
	st := m.status.data

	s.section("This peer")
	s.field("FQDN", st.FQDN)
	s.field("IPv4", st.IP)
	s.field("IPv6", st.IPv6)
	s.field("Public key", st.PublicKey)
	iface := "userspace"
	if st.KernelInterface {
		iface = "kernel"
	}
	s.field("WireGuard", fmt.Sprintf("%s interface, port %d", iface, st.WireguardPort))
	s.field("Profile", st.Profile)
	if p, ok := m.profiles.data.Active(); ok && p.Email != "" {
		s.field("Account", p.Email)
	}

	m.connectionSection(s, st)

	s.section("Features")
	s.field("Lazy connections", yesNo(st.LazyConnection))
	qr := yesNo(st.QuantumResistance)
	if st.QuantumResistancePermissive {
		qr += dim.Render(" (permissive)")
	}
	s.field("Quantum resistance", qr)
	sshSrv := yesNo(st.SSHServer)
	if st.SSHServer {
		sshSrv += fmt.Sprintf(", %d sessions", st.SSHSessions)
	}
	s.field("SSH server", sshSrv)

	s.section("Relays")
	var relays []string
	for _, r := range st.Relays {
		line := r.URI
		if r.Transport != "" {
			line += dim.Render(" (" + r.Transport + ")")
		}
		if r.Available {
			line += "  " + good.Render("available")
		} else {
			line += "  " + errStyle.Render("unavailable")
		}
		if r.Error != "" {
			line += "  " + errStyle.Render(r.Error)
		}
		relays = append(relays, line)
	}
	s.list(fmt.Sprintf("%d of %d up", st.RelaysAvailable(), len(st.Relays)), relays)

	s.section("Networks")
	s.list("Routes", st.Networks)
	var dns []string
	for _, d := range st.DNS {
		line := strings.Join(d.Servers, ", ")
		if len(d.Domains) > 0 {
			line += dim.Render(" for " + strings.Join(d.Domains, ", "))
		}
		if !d.Enabled {
			line += dim.Render(" (disabled)")
		}
		if d.Error != "" {
			line += "  " + errStyle.Render(d.Error)
		}
		dns = append(dns, line)
	}
	s.list("Nameservers", dns)

	s.section("Versions")
	s.field("Client (CLI)", st.CLIVersion)
	s.field("Daemon", st.DaemonVersion)

	s.section("Recent events")
	var events []string
	for _, e := range st.Events {
		line := fmtTime(e.Time) + "  " + e.Message
		if e.Severity != "" && e.Severity != "INFO" {
			line = warn.Render(e.Severity) + " " + line
		}
		events = append(events, line)
	}
	s.list("", events)
	return s.String()
}

func (m Model) peerDetail(s *sheet, p core.Peer) {
	now := m.now()
	s.section("Peer")
	s.field("Name", p.Name())
	s.field("FQDN", p.FQDN)
	s.field("IPv4", p.IP)
	s.field("IPv6", p.IPv6)
	s.field("Public key", p.PublicKey)

	s.section("Connection")
	status := peerStatusLabel(p)
	if p.Connected() {
		status = good.Render(status)
	}
	if !p.StatusChanged.IsZero() {
		status += dim.Render(" since " + fmtTime(p.StatusChanged) + " (" + ago(p.StatusChanged, now) + ")")
	}
	s.field("Status", status)
	if p.Connected() || p.ConnType != "" {
		s.field("Type", p.ConnType)
		s.field("ICE local", strings.TrimSpace(p.LocalICE+"  "+dim.Render(p.LocalEndpoint)))
		s.field("ICE remote", strings.TrimSpace(p.RemoteICE+"  "+dim.Render(p.RemoteEndpoint)))
		if p.Relay != "" {
			s.field("Relay", p.Relay)
		}
		s.field("Latency", fmtLatency(p.Latency))
		hs := ""
		if !p.LastHandshake.IsZero() {
			hs = fmtTime(p.LastHandshake) + dim.Render(" ("+ago(p.LastHandshake, now)+")")
		}
		s.field("Last handshake", hs)
		s.field("Received / sent", strings.TrimSpace(fmtBytes(p.Received)+" / "+fmtBytes(p.Sent)))
		s.field("Quantum resistant", yesNo(p.Quantum))
	}
	if len(p.Networks) > 0 {
		s.list("Routes", p.Networks)
	}

	s.section("Access")
	s.field("ssh (s)", strings.Join(m.launch.SSHArgv(p), " ")+dim.Render("  in "+string(m.launch.SSHMode())))
	if argv, _, err := m.launch.RDPArgv(p); err != nil {
		s.field("rdp (d)", dim.Render(err.Error()))
	} else {
		s.field("rdp (d)", strings.Join(argv, " "))
	}

	m.scanSection(s, p)
}

func (m Model) scanSection(s *sheet, p core.Peer) {
	s.section("Port scan (nmap)")
	r := m.scans[p.IP]
	if r == nil {
		s.field("", dim.Render("n quick scan (top 100 ports) · N full scan with service versions (top 1000)"))
		return
	}
	if r.loading {
		s.field("Status", m.spinner.View()+" scanning…")
		return
	}
	sc := r.data
	s.field("Command", "nmap "+strings.Join(sc.Args, " "))
	s.field("Scanned", fmtTime(sc.FetchedAt)+dim.Render(fmt.Sprintf(" (took %s)", sc.Elapsed.Round(100_000_000))))
	s.errField("Error", sc.Err)
	if sc.Summary != "" {
		s.field("Summary", sc.Summary)
	}
	if !sc.HostUp && sc.Err == nil {
		s.field("Host", warn.Render("did not respond"))
	}
	var ports []string
	for _, pt := range sc.Ports {
		state := pt.State
		if state == "open" {
			state = good.Render(state)
		} else {
			state = dim.Render(state)
		}
		line := fmt.Sprintf("%-9s %s  %s", fmt.Sprintf("%d/%s", pt.Number, pt.Protocol), state, pt.Service)
		if d := pt.Description(); d != "" {
			line += dim.Render("  " + d)
		}
		ports = append(ports, line)
	}
	s.list("Ports", ports)
	if sc.Closed > 0 {
		s.field("", dim.Render(fmt.Sprintf("%d more ports closed or filtered", sc.Closed)))
	}
}
