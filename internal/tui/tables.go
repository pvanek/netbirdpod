package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pvanek/netbirdpod/internal/core"
)

// col is a column with a minimum width, a share of any leftover width and
// an alignment: numbers right, text left.
type col struct {
	title  string
	min    int
	weight int
	align  align
}

type align bool

const (
	left  align = false
	right align = true
)

// rebuildTable recomputes columns and rows for the active tab, and the
// This peer sheet.
func (m *Model) rebuildTable() {
	cols, rows, items := m.tableData()
	m.items = items

	// Clear rows first: the table indexes rows by column, so a shorter
	// column set with old rows would render out of range. SetRows(nil)
	// resets the cursor, so restore it afterwards.
	cursor := m.table.Cursor()
	m.table.SetRows(nil)
	laid := layout(cols, m.width)
	m.table.SetColumns(laid)
	m.table.SetRows(alignRows(cols, laid, rows))
	m.table.SetCursor(min(cursor, max(len(rows)-1, 0)))

	used := lipgloss.Height(m.header()) + 2 // + footer, table header border
	m.table.SetWidth(max(m.width, 20))
	m.table.SetHeight(max(m.height-used, 3))

	m.info.SetWidth(max(m.width, 20))
	m.info.SetHeight(max(m.height-lipgloss.Height(m.header())-1, 3))
	m.info.SetContent(m.thisPeerBody())

	m.syncDetail()
	m.syncHelp()
}

func layout(cols []col, width int) []table.Column {
	// Each cell gets 2 chars of padding from the default styles.
	avail := width - 2*len(cols)
	total, weights := 0, 0
	for _, c := range cols {
		total += c.min
		weights += c.weight
	}
	extra := max(avail-total, 0)
	out := make([]table.Column, len(cols))
	for i, c := range cols {
		w := c.min
		switch {
		case total > avail:
			// Too narrow: shrink proportionally.
			w = max(3, c.min*max(avail, 0)/total)
		case weights > 0:
			w += extra * c.weight / weights
		}
		out[i] = table.Column{Title: alignCell(c.title, w, c.align), Width: w}
	}
	return out
}

// alignRows right-aligns the cells of right-aligned columns; the table
// itself only aligns left.
func alignRows(cols []col, laid []table.Column, rows []table.Row) []table.Row {
	out := make([]table.Row, len(rows))
	for r, row := range rows {
		out[r] = make(table.Row, len(row))
		for i, v := range row {
			if i < len(cols) && i < len(laid) {
				v = alignCell(v, laid[i].Width, cols[i].align)
			}
			out[r][i] = v
		}
	}
	return out
}

// alignCell pads text on the left to width when right-aligned. Text too
// wide is left as is for the table to shorten.
func alignCell(s string, width int, a align) string {
	if a == right {
		if w := ansi.StringWidth(s); w < width {
			return strings.Repeat(" ", width-w) + s
		}
	}
	return s
}

// pick keeps the given columns of cols and rows.
func pick(cols []col, rows []table.Row, keep ...int) ([]col, []table.Row) {
	outCols := make([]col, len(keep))
	for j, k := range keep {
		outCols[j] = cols[k]
	}
	outRows := make([]table.Row, len(rows))
	for i, r := range rows {
		outRows[i] = make(table.Row, len(keep))
		for j, k := range keep {
			outRows[i][j] = r[k]
		}
	}
	return outCols, outRows
}

// narrowWidth is below which tables drop their secondary columns.
const narrowWidth = 100

func (m Model) tableData() ([]col, []table.Row, []any) {
	switch m.tab {
	case tabPeers:
		return m.peersTable()
	case tabThisPeer:
		return nil, nil, nil
	}
	return m.connectionsTable()
}

// --- connections ---

// connectionsTable lists the profiles. NetBird reports the state of the
// active profile only; the others are just listed.
func (m Model) connectionsTable() ([]col, []table.Row, []any) {
	cols := []col{
		{"", 1, 0, left}, {"Profile", 10, 2, left}, {"Account", 10, 3, left}, {"Status", 14, 1, left},
		{"Management", 14, 3, left}, {"This peer", 12, 3, left}, {"IP", 12, 1, left},
		{"Peers", 6, 0, right}, {"Session", 9, 0, left},
	}
	var (
		rows  []table.Row
		items []any
	)
	st := m.status.data
	stOK := m.status.loaded && st.Err == nil
	now := m.now()
	for _, p := range m.profiles.data.Profiles {
		row := make(table.Row, len(cols))
		row[1], row[2] = p.Name, p.Email
		if p.Active {
			row[0] = "✓"
			if stOK {
				row[3] = daemonLabel(st.Daemon)
				row[4] = shortURL(st.Management.URL)
				row[5] = st.FQDN
				row[6] = stripMask(st.IP)
				if len(st.Peers) > 0 {
					row[7] = fmt.Sprintf("%d/%d", st.PeersConnected, len(st.Peers))
				}
				row[8] = until(st.SessionExpiresAt, now)
			} else if m.status.loading {
				row[3] = "…"
			}
		}
		rows = append(rows, row)
		items = append(items, p)
	}
	if m.width < narrowWidth {
		cols, rows = pick(cols, rows, 0, 1, 2, 3, 7)
	}
	return cols, rows, items
}

// shortURL drops the https scheme and default port of a service URL.
func shortURL(u string) string {
	if rest, ok := strings.CutPrefix(u, "https://"); ok {
		return strings.TrimSuffix(rest, ":443")
	}
	return u
}

// --- peers ---

func (m Model) visiblePeers() []core.Peer {
	var out []core.Peer
	for _, p := range m.status.data.Peers {
		if !m.connectedOnly || p.Connected() {
			out = append(out, p)
		}
	}
	return out
}

func (m Model) peersNote() string {
	st := m.status.data
	if !m.status.loaded || st.Err != nil {
		return dim.Render("–")
	}
	filter := "all peers"
	if m.connectedOnly {
		filter = scopeStyle.Render("connected only")
	}
	note := fmt.Sprintf("%d peers, %d connected · showing %s (c)", len(st.Peers), st.PeersConnected, filter)
	if st.LazyConnection {
		note += " · lazy connections: idle peers connect on first traffic"
	}
	return dim.Width(max(m.width, 20)).Render(note)
}

func (m Model) peersTable() ([]col, []table.Row, []any) {
	cols := []col{
		{"Peer", 12, 3, left}, {"IP", 15, 0, left}, {"Status", 11, 0, left}, {"Type", 7, 0, left},
		{"Latency", 7, 0, right}, {"Handshake", 9, 0, left}, {"Rx", 8, 0, right}, {"Tx", 8, 0, right},
		{"Routes", 6, 1, left}, {"Open ports", 10, 2, left},
	}
	now := m.now()
	var (
		rows  []table.Row
		items []any
	)
	for _, p := range m.visiblePeers() {
		rows = append(rows, table.Row{
			p.Name(), p.IP, peerStatusLabel(p), p.ConnType, fmtLatency(p.Latency), ago(p.LastHandshake, now),
			fmtBytes(p.Received), fmtBytes(p.Sent), strings.Join(p.Networks, ","), m.openPortsCell(p.IP),
		})
		items = append(items, p)
	}
	if m.width < narrowWidth {
		cols, rows = pick(cols, rows, 0, 1, 2, 4, 9)
	}
	return cols, rows, items
}

func peerStatusLabel(p core.Peer) string {
	switch p.Status {
	case core.DaemonConnected:
		return "● " + p.Status
	case core.DaemonConnecting:
		return "◐ " + p.Status
	case core.DaemonIdle:
		return "○ Idle"
	}
	return p.Status
}

// openPortsCell summarises the last scan of ip.
func (m Model) openPortsCell(ip string) string {
	r := m.scans[ip]
	switch {
	case r == nil:
		return ""
	case r.loading:
		return "scanning…"
	case r.data.Err != nil && len(r.data.Ports) == 0:
		return "scan failed"
	}
	open := r.data.Open()
	if len(open) == 0 {
		return "none"
	}
	ports := make([]string, len(open))
	for i, p := range open {
		ports[i] = strconv.Itoa(p.Number)
	}
	return strings.Join(ports, ",")
}
