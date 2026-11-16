package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pvanek/netbirdpod/internal/core"
)

// Data is loaded lazily: each screen loads only the data sets it shows,
// the first time they are needed. "r" and auto-refresh reload the visible
// screen only. Scans run only on request.

// dataset is one kind of data.
type dataset int

const (
	dsStatus   dataset = iota // netbird status: this peer and its peers
	dsProfiles                // netbird profile list
)

const (
	fetchTimeout = 30 * time.Second
	scanTimeout  = 15 * time.Minute
)

// tabData lists the data sets each tab shows.
var tabData = [tabCount][]dataset{
	tabConnections: {dsProfiles, dsStatus},
	tabThisPeer:    {dsStatus},
	tabPeers:       {dsStatus},
}

// resource is the load state of one data set.
type resource[T any] struct {
	loading bool
	loaded  bool
	data    T
}

// loadedMsg carries a data set back into Update.
type loadedMsg[T any] struct {
	data T
}

// scanMsg carries a finished scan back into Update.
type scanMsg struct {
	data core.Scan
}

// load starts fetching r unless a fetch is already running.
func load[T any](r *resource[T], fetch func(ctx context.Context, now time.Time) T, now time.Time) tea.Cmd {
	if r.loading {
		return nil
	}
	r.loading = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		return loadedMsg[T]{data: fetch(ctx, now)}
	}
}

// loadSet fetches data set ds; unless force, only if it was never loaded.
func (m *Model) loadSet(ds dataset, force bool) tea.Cmd {
	now := m.now()
	switch ds {
	case dsStatus:
		if force || !m.status.loaded {
			return load(&m.status, m.svc.Status, now)
		}
	case dsProfiles:
		if force || !m.profiles.loaded {
			return load(&m.profiles, m.svc.Profiles, now)
		}
	}
	return nil
}

// scan starts an nmap scan of ip unless one is running.
func (m *Model) scan(ip string, full bool) tea.Cmd {
	if ip == "" {
		return nil
	}
	r := m.scans[ip]
	if r == nil {
		r = &resource[core.Scan]{}
		m.scans[ip] = r
	}
	if r.loading {
		m.setFlash(flashWarn, "a scan of "+ip+" is already running")
		return nil
	}
	r.loading = true
	svc, now := m.svc, m.now()
	kind := "quick"
	if full {
		kind = "full"
	}
	m.setFlash(flashInfo, fmt.Sprintf("%s scan of %s started", kind, ip))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		return scanMsg{data: svc.Scan(ctx, ip, full, now)}
	}
}

// visibleData lists the data sets the visible screen shows.
func (m Model) visibleData() []dataset {
	return tabData[m.tab]
}

// ensureVisible loads what the visible screen shows and was never loaded.
func (m *Model) ensureVisible() tea.Cmd { return m.loadVisible(false) }

// refreshVisible reloads everything the visible screen shows.
func (m *Model) refreshVisible() tea.Cmd { return m.loadVisible(true) }

// refreshAll reloads every data set, after an action changed the state.
func (m *Model) refreshAll() tea.Cmd {
	return tea.Batch(m.loadSet(dsStatus, true), m.loadSet(dsProfiles, true))
}

func (m *Model) loadVisible(force bool) tea.Cmd {
	var cmds []tea.Cmd
	for _, ds := range m.visibleData() {
		cmds = append(cmds, m.loadSet(ds, force))
	}
	return tea.Batch(cmds...)
}

// setState is the load state of one data set.
type setState struct {
	loading, loaded bool
	fetchedAt       time.Time
	err             error
}

func (m Model) state(ds dataset) setState {
	switch ds {
	case dsStatus:
		r := m.status
		return setState{r.loading, r.loaded, r.data.FetchedAt, r.data.Err}
	case dsProfiles:
		r := m.profiles
		return setState{r.loading, r.loaded, r.data.FetchedAt, r.data.Err}
	}
	return setState{}
}

func (m Model) visibleLoading() bool {
	for _, ds := range m.visibleData() {
		if m.state(ds).loading {
			return true
		}
	}
	return false
}

func (m Model) visibleErr() error {
	var errs []error
	for _, ds := range m.visibleData() {
		if err := m.state(ds).err; err != nil && !containsErr(errs, err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// containsErr avoids repeating one cause, e.g. a stopped daemon failing
// both the status and the profile list.
func containsErr(errs []error, err error) bool {
	for _, e := range errs {
		if e.Error() == err.Error() {
			return true
		}
	}
	return false
}

// lastFetch is when the oldest visible data was fetched, so "updated"
// never overstates freshness.
func (m Model) lastFetch() time.Time {
	var t time.Time
	for _, ds := range m.visibleData() {
		if s := m.state(ds); s.loaded && (t.IsZero() || s.fetchedAt.Before(t)) {
			t = s.fetchedAt
		}
	}
	return t
}
