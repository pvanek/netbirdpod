package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pvanek/netbirdpod/internal/core"
)

var now = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

type fakeFetcher struct {
	calls *[]string
}

func (f fakeFetcher) record(s string) {
	if f.calls != nil {
		*f.calls = append(*f.calls, s)
	}
}

var testStatus = core.Status{
	FetchedAt: now, Daemon: core.DaemonConnected, Profile: "work",
	FQDN: "me.nb.example.com", IP: "100.64.0.1/16", PublicKey: "pk-me", KernelInterface: true, WireguardPort: 51820,
	Management:       core.Endpoint{URL: "https://api.example.com:443", Connected: true},
	Signal:           core.Endpoint{URL: "https://signal.example.com:443", Connected: true},
	Relays:           []core.Relay{{URI: "rels://relay.example.com:443", Available: true, Transport: "ws"}},
	SessionExpiresAt: now.Add(6 * 24 * time.Hour),
	LazyConnection:   true,
	Events:           []core.Event{{Time: now.Add(-time.Hour), Severity: "INFO", Message: "Network map updated"}},
	PeersConnected:   1,
	Peers: []core.Peer{
		{FQDN: "db.nb.example.com", IP: "100.64.0.3", PublicKey: "pk-db", Status: "Idle"},
		{FQDN: "web.nb.example.com", IP: "100.64.0.2", PublicKey: "pk-web", Status: "Connected", ConnType: "P2P",
			Latency: 12 * time.Millisecond, LastHandshake: now.Add(-2 * time.Minute), Received: 2048, Sent: 1024,
			Networks: []string{"10.0.0.0/24"}},
	},
}

func (f fakeFetcher) Status(context.Context, time.Time) core.Status { return testStatus }

func (f fakeFetcher) Profiles(context.Context, time.Time) core.ProfileList {
	return core.ProfileList{FetchedAt: now, Profiles: []core.Profile{
		{Name: "home"}, {Name: "work", Active: true, Email: "me@example.com"},
	}}
}

func (f fakeFetcher) Scan(_ context.Context, ip string, full bool, _ time.Time) core.Scan {
	return core.Scan{IP: ip, Full: full, FetchedAt: now, HostUp: true, Args: []string{"-F", ip},
		Ports: []core.Port{{Protocol: "tcp", Number: 22, State: "open", Service: "ssh", Product: "OpenSSH"}}}
}

func (f fakeFetcher) ScanArgs(ip string, _ bool) []string { return []string{ip} }

func (f fakeFetcher) Down(context.Context) error { f.record("down"); return nil }

func (f fakeFetcher) SelectProfile(_ context.Context, name string) error {
	f.record("select " + name)
	return nil
}

func (f fakeFetcher) UpCommand(profile string) *exec.Cmd {
	f.record("up " + profile)
	return exec.Command("true")
}

func (f fakeFetcher) LoginCommand(profile string) *exec.Cmd {
	f.record("login " + profile)
	return exec.Command("true")
}

type fakeLauncher struct {
	opened *[]string
	locked bool // like Konsole without its D-Bus option
}

func (l fakeLauncher) Available(mode core.Mode) error {
	if l.locked && mode != core.ModeHere && mode != core.ModeWindow {
		return errors.New("Konsole blocks running commands over D-Bus")
	}
	return nil
}

func (l fakeLauncher) SSH(p core.Peer, mode core.Mode) (core.Result, error) {
	*l.opened = append(*l.opened, "ssh "+p.Name()+" "+string(mode))
	if mode == core.ModeHere {
		return core.Result{Inline: exec.Command("true")}, nil
	}
	return core.Result{Where: "new Konsole tab"}, nil
}

func (l fakeLauncher) RDP(p core.Peer) (core.Result, error) {
	return core.Result{}, errors.New("no RDP client found")
}

func (l fakeLauncher) SSHMode() core.Mode { return core.ModeTab }

func (l fakeLauncher) SSHArgv(p core.Peer) []string { return []string{"ssh", p.IP} }

func (l fakeLauncher) RDPArgv(core.Peer) ([]string, bool, error) {
	return nil, false, errors.New("no RDP client found")
}

type harness struct {
	calls  []string
	opened []string
}

// loaded returns a model with all data loaded, and the recorders.
func loaded(t *testing.T, width int) (Model, *harness) {
	t.Helper()
	h := &harness{}
	f := fakeFetcher{calls: &h.calls}
	m := New(f, fakeLauncher{opened: &h.opened}, time.Minute)
	m.now = func() time.Time { return now }
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	tm, _ = tm.Update(loadedMsg[core.Status]{data: f.Status(context.Background(), now)})
	tm, _ = tm.Update(loadedMsg[core.ProfileList]{data: f.Profiles(context.Background(), now)})
	return tm.(Model), h
}

// press sends keys and runs the commands they return that are not
// terminal programs, feeding their messages back.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	var tm tea.Model = m
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		tm, _ = tm.Update(msg)
	}
	return tm.(Model)
}

// run executes cmd and feeds back the messages it produces (one level of
// batches); terminal programs are reported, not run.
func run(t *testing.T, m Model, cmd tea.Cmd) (Model, []tea.Msg) {
	t.Helper()
	var msgs []tea.Msg
	var collect func(c tea.Cmd)
	collect = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				collect(c)
			}
		case nil:
		default:
			msgs = append(msgs, msg)
		}
	}
	collect(cmd)
	var tm tea.Model = m
	for _, msg := range msgs {
		if _, ok := msg.(autoTickMsg); ok {
			continue
		}
		if strings.Contains(fmt.Sprintf("%T", msg), "execMsg") {
			continue // a terminal program, such as netbird up
		}
		tm, _ = tm.Update(msg)
	}
	return tm.(Model), msgs
}

func assertContains(t *testing.T, out string, want ...string) {
	t.Helper()
	plain := ansi.Strip(out)
	for _, w := range want {
		if !strings.Contains(plain, w) {
			t.Errorf("missing %q in:\n%s", w, plain)
		}
	}
}

func TestConnectionsTab(t *testing.T) {
	m, _ := loaded(t, 160)
	out := m.render()
	assertContains(t, out, "0 Connections", "Profile: work", "● Connected", "me.nb.example.com",
		"home", "work", "me@example.com", "api.example.com", "100.64.0.1", "1/2", "in 6d", "a auto 1m")
}

func TestThisPeerTab(t *testing.T) {
	m, _ := loaded(t, 120)
	m = press(t, m, "1")
	assertContains(t, m.render(), "This peer", "pk-me", "kernel interface, port 51820", "rels://relay.example.com:443",
		"1 of 1 up", "Lazy connections", "Network map updated", "Session expires")
}

func TestPeersTabAndFilter(t *testing.T) {
	m, _ := loaded(t, 160)
	m = press(t, m, "2")
	out := m.render()
	assertContains(t, out, "db", "web", "100.64.0.2", "● Connected", "P2P", "12ms", "2m ago", "2.0 KiB", "10.0.0.0/24")
	m = press(t, m, "c")
	if len(m.items) != 1 || m.items[0].(core.Peer).Name() != "web" {
		t.Fatalf("connected-only filter: %v", m.items)
	}
	assertContains(t, m.render(), "connected only")
}

func TestPeerDetailScanAndSSH(t *testing.T) {
	m, h := loaded(t, 140)
	m = press(t, m, "2", "down", "enter") // web
	assertContains(t, m.render(), "Peer web", "ssh 100.64.0.2", "n quick scan", "no RDP client found")

	// n starts a scan; its result shows in the detail and in the table.
	var tm tea.Model = m
	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m, _ = run(t, tm.(Model), cmd)
	assertContains(t, m.render(), "22/tcp", "OpenSSH", "scan of 100.64.0.2 done: 1 open ports")
	m = press(t, m, "esc")
	assertContains(t, m.render(), "22")

	// s opens ssh in the default mode; S picks one.
	m = press(t, m, "s")
	assertContains(t, m.render(), "ssh web opened in new Konsole tab")
	m = press(t, m, "S")
	assertContains(t, m.render(), "ssh web in: 1 here", "3 tab*")
	m = press(t, m, "4")
	if want := []string{"ssh web tab", "ssh web split-right"}; strings.Join(h.opened, ",") != strings.Join(want, ",") {
		t.Fatalf("opened %v, want %v", h.opened, want)
	}
	m = press(t, m, "d")
	assertContains(t, m.render(), "rdp web: no RDP client found")
}

func TestMenuMarksFallbacks(t *testing.T) {
	m, h := loaded(t, 200)
	m = press(t, m, "2", "S")
	if out := ansi.Strip(m.render()); strings.Contains(out, "→window") {
		t.Fatalf("modes marked although the terminal can open them:\n%s", out)
	}
	m = press(t, m, "esc")

	m.launch = fakeLauncher{opened: &h.opened, locked: true}
	m = press(t, m, "S")
	assertContains(t, m.render(), "1 here ·", "2 window ·", "3 tab→window*", "4 split right→window", "5 split down→window",
		"tab and split open a new window: Konsole blocks running commands over D-Bus")
	m = press(t, m, "esc")
	if m.menu != nil || m.flash.text != "" {
		t.Fatal("esc left the menu or its warning open")
	}
}

func TestDownAsksFirst(t *testing.T) {
	m, h := loaded(t, 140)
	m = press(t, m, "D")
	assertContains(t, m.render(), `Disconnect "work" from NetBird?`)
	m = press(t, m, "n")
	if m.confirm != nil || len(h.calls) != 0 {
		t.Fatalf("cancel did not cancel: %v", h.calls)
	}
	var tm tea.Model = press(t, m, "D")
	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m, _ = run(t, tm.(Model), cmd)
	if strings.Join(h.calls, ",") != "down" {
		t.Fatalf("calls %v", h.calls)
	}
	assertContains(t, m.render(), "disconnected")
}

func TestUpSwitchesProfile(t *testing.T) {
	m, h := loaded(t, 140)
	// Row 0 is "home", another profile than the connected "work".
	var tm tea.Model = press(t, m, "u")
	assertContains(t, tm.(Model).render(), `Disconnect "work" and connect "home"?`)
	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m, msgs := run(t, tm.(Model), cmd)
	if len(msgs) != 1 {
		t.Fatalf("msgs %v", msgs)
	}
	// The switch message chains "netbird up --profile home" in the terminal.
	if strings.Join(h.calls, ",") != "down,select home,up home" {
		t.Fatalf("calls %v", h.calls)
	}
	assertContains(t, m.render(), "switched to home")

	// u on the connected profile does nothing.
	m = press(t, m, "down", "u")
	assertContains(t, m.render(), "already connected")
}

func TestNarrowAndHelp(t *testing.T) {
	m, _ := loaded(t, 60)
	for _, k := range []string{"0", "1", "2"} {
		m = press(t, m, k)
		for _, line := range strings.Split(m.render(), "\n") {
			if w := ansi.StringWidth(line); w > 60 {
				t.Fatalf("tab %s: line wider than the screen (%d): %q", k, w, ansi.Strip(line))
			}
		}
	}
	m = press(t, m, "?")
	assertContains(t, m.render(), "Keys", "remote desktop")
	m = press(t, m, "esc")
	if m.help != nil {
		t.Fatal("help not closed")
	}
}

func TestStatusError(t *testing.T) {
	m, _ := loaded(t, 120)
	var tm tea.Model = m
	tm, _ = tm.Update(loadedMsg[core.Status]{data: core.Status{FetchedAt: now, Err: errors.New("netbird status: daemon is not running")}})
	out := tm.(Model).render()
	assertContains(t, out, "unavailable", "daemon is not running")
}

func TestAutoRefresh(t *testing.T) {
	m, _ := loaded(t, 120)
	m.now = func() time.Time { return now.Add(30 * time.Second) }
	if cmd := m.onAutoTick(); cmd != nil {
		t.Fatal("refreshed before the interval")
	}
	m.now = func() time.Time { return now.Add(time.Minute) }
	if cmd := m.onAutoTick(); cmd == nil {
		t.Fatal("no refresh after the interval")
	}
}
