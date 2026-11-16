package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pvanek/netbirdpod/internal/netbird"
)

var now = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func TestConvertStatus(t *testing.T) {
	raw, err := netbird.ParseStatus([]byte(`{"peers":{"total":2,"connected":1,"details":[
		{"fqdn":"zeta.nb.example.com","netbirdIp":"100.64.0.3","status":"Idle","connectionType":"-",
		 "lastStatusUpdate":"0001-01-01T00:57:44+00:57","lastWireguardHandshake":"0001-01-01T00:00:00Z"},
		{"fqdn":"alpha.nb.example.com","netbirdIp":"100.64.0.2","status":"Connected","connectionType":"P2P","latency":5000000}]},
		"daemonStatus":"Connected","events":[
		{"message":"old","timestamp":"2026-11-16T07:00:00Z"},{"message":"new","userMessage":"New!","timestamp":"2026-11-16T08:00:00Z"}],
		"sessionExpiresAt":"0001-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	st := convertStatus(raw, now)
	if !st.Connected() || st.NeedsLogin() {
		t.Fatalf("daemon state: %q", st.Daemon)
	}
	if st.Peers[0].Name() != "alpha" || st.Peers[1].Name() != "zeta" {
		t.Fatalf("peers not sorted by FQDN: %v, %v", st.Peers[0].FQDN, st.Peers[1].FQDN)
	}
	z := st.Peers[1]
	if !z.StatusChanged.IsZero() || !z.LastHandshake.IsZero() || z.ConnType != "" {
		t.Fatalf("NetBird's never and '-' not cleared: %+v", z)
	}
	if st.Peers[0].Latency != 5*time.Millisecond {
		t.Fatalf("latency %v", st.Peers[0].Latency)
	}
	if !st.SessionExpiresAt.IsZero() {
		t.Fatal("zero session expiry not cleared")
	}
	if st.Events[0].Message != "New!" {
		t.Fatalf("events not newest first with user message: %+v", st.Events)
	}
}

const nmapXML = `<?xml version="1.0"?>
<nmaprun args="nmap -oX - -Pn -T4 -F 100.64.0.2">
<host><status state="up"/>
<ports><extraports state="closed" count="97"/>
<port protocol="tcp" portid="22"><state state="open"/><service name="ssh" product="OpenSSH" version="9.6p1" extrainfo="Ubuntu"/></port>
<port protocol="tcp" portid="80"><state state="filtered"/><service name="http"/></port>
<port protocol="tcp" portid="3389"><state state="open"/><service name="ms-wbt-server"/></port>
</ports></host>
<runstats><finished elapsed="1.50" summary="Nmap done; 1 IP address (1 host up) scanned in 1.50 seconds"/></runstats>
</nmaprun>`

func TestScan(t *testing.T) {
	s := NewService(Options{}, netbird.New("", func(context.Context, ...string) ([]byte, error) { return nil, nil }))
	var gotArgs []string
	s.runNmap = func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(nmapXML), nil
	}
	sc := s.Scan(context.Background(), "100.64.0.2", false, now)
	if sc.Err != nil {
		t.Fatal(sc.Err)
	}
	if want := []string{"-oX", "-", "-Pn", "-T4", "-F", "100.64.0.2"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args %v, want %v", gotArgs, want)
	}
	if !sc.HostUp || len(sc.Ports) != 3 || len(sc.Open()) != 2 || sc.Closed != 97 || sc.Elapsed != 1500*time.Millisecond {
		t.Fatalf("unexpected scan: %+v", sc)
	}
	if !sc.HasOpen(22) || sc.HasOpen(80) || !sc.HasOpen(3389) {
		t.Fatal("HasOpen wrong")
	}
	if d := sc.Ports[0].Description(); d != "OpenSSH 9.6p1 Ubuntu" {
		t.Fatalf("description %q", d)
	}

	s.runNmap = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("nmap is not installed") }
	if sc := s.Scan(context.Background(), "100.64.0.2", true, now); sc.Err == nil || !sc.Full {
		t.Fatalf("want error and full flag: %+v", sc)
	}
}

// fakeEnv builds a launcher with a fixed environment and installed tools,
// recording what it starts and runs.
type fakeEnv struct {
	env       map[string]string
	parents   []string // parent process names
	goos      string
	installed []string
	started   [][]string
	ran       [][]string
	bus       *fakeKonsole
}

func (f *fakeEnv) launcher(opts LaunchOptions) *Launcher {
	l := NewLauncher(opts)
	l.goos = "linux"
	if f.goos != "" {
		l.goos = f.goos
	}
	l.parents = func() []string { return f.parents }
	l.getenv = func(k string) string { return f.env[k] }
	l.lookPath = func(bin string) (string, error) {
		for _, b := range f.installed {
			if b == bin {
				return "/usr/bin/" + bin, nil
			}
		}
		return "", errors.New("not found")
	}
	l.start = func(argv []string) error { f.started = append(f.started, argv); return nil }
	l.run = func(argv []string) error { f.ran = append(f.ran, argv); return nil }
	l.konsole = func(string) (KonsoleBus, error) {
		if f.bus == nil {
			return nil, errors.New("no bus")
		}
		return f.bus, nil
	}
	return l
}

var peer = Peer{FQDN: "web.nb.example.com", IP: "100.64.0.9"}

func TestSSHArgvAndPeerSettings(t *testing.T) {
	f := &fakeEnv{}
	l := f.launcher(LaunchOptions{SSHUser: "admin", Peers: map[string]PeerSettings{"web": {SSHUser: "root"}}})
	if got := l.SSHArgv(peer); !reflect.DeepEqual(got, []string{"ssh", "root@100.64.0.9"}) {
		t.Fatalf("got %v", got)
	}
	l = f.launcher(LaunchOptions{SSHCommand: []string{"netbird", "ssh"}, UseFQDN: true})
	if got := l.SSHArgv(peer); !reflect.DeepEqual(got, []string{"netbird", "ssh", "web.nb.example.com"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOpenHere(t *testing.T) {
	l := (&fakeEnv{}).launcher(LaunchOptions{})
	res, err := l.SSH(peer, ModeHere)
	if err != nil || res.Inline == nil || strings.Join(res.Inline.Args, " ") != "ssh 100.64.0.9" {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestOpenTmux(t *testing.T) {
	f := &fakeEnv{env: map[string]string{"TMUX": "/tmp/tmux", "KONSOLE_DBUS_SERVICE": ":1.2"}}
	l := f.launcher(LaunchOptions{})
	if _, err := l.SSH(peer, ModeSplitRight); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tmux", "split-window", "-h", "ssh 100.64.0.9"}; !reflect.DeepEqual(f.ran[0], want) {
		t.Fatalf("ran %v", f.ran)
	}
}

func TestOpenWindowFallsBack(t *testing.T) {
	// No terminal to control: a tab becomes a window, with a note.
	f := &fakeEnv{env: map[string]string{}, installed: []string{"xterm"}}
	l := f.launcher(LaunchOptions{})
	res, err := l.SSH(peer, ModeTab)
	if err != nil {
		t.Fatal(err)
	}
	if res.Note == "" || !reflect.DeepEqual(f.started[0], []string{"xterm", "-e", "ssh", "100.64.0.9"}) {
		t.Fatalf("res %+v started %v", res, f.started)
	}
	// A configured terminal wins.
	f = &fakeEnv{env: map[string]string{"KONSOLE_VERSION": "260803"}, installed: []string{"konsole"}}
	l = f.launcher(LaunchOptions{Terminal: []string{"foot"}})
	if _, err := l.SSH(peer, ModeWindow); err != nil || f.started[0][0] != "foot" {
		t.Fatalf("started %v err %v", f.started, err)
	}
	// Nothing at all.
	f = &fakeEnv{env: map[string]string{}}
	if _, err := f.launcher(LaunchOptions{}).SSH(peer, ModeWindow); err == nil {
		t.Fatal("want error without terminals")
	}
}

type fakeKonsole struct {
	locked   bool
	sessions []int
	hier     string
	splits   []string
	commands map[int]string
}

func (k *fakeKonsole) CanRunCommand(string) (bool, error) { return !k.locked, nil }
func (k *fakeKonsole) NewSession(string) (int, error) {
	id := k.sessions[len(k.sessions)-1] + 1
	k.sessions = append(k.sessions, id)
	return id, nil
}
func (k *fakeKonsole) SessionList(string) ([]int, error)    { return append([]int{}, k.sessions...), nil }
func (k *fakeKonsole) ViewHierarchy(string) (string, error) { return k.hier, nil }
func (k *fakeKonsole) CreateSplit(_ string, view int, horizontal bool) error {
	k.splits = append(k.splits, strings.Join([]string{string(rune('0' + view)), map[bool]string{true: "h", false: "v"}[horizontal]}, ""))
	_, err := k.NewSession("")
	return err
}
func (k *fakeKonsole) RunCommand(id int, cmd string) error {
	if k.commands == nil {
		k.commands = map[int]string{}
	}
	k.commands[id] = cmd
	return nil
}
func (k *fakeKonsole) SetTabTitle(int, string) error { return nil }
func (k *fakeKonsole) Close() error                  { return nil }

func konsoleEnv(bus *fakeKonsole) *fakeEnv {
	return &fakeEnv{
		env: map[string]string{"KONSOLE_DBUS_SERVICE": ":1.186", "KONSOLE_DBUS_WINDOW": "/Windows/1",
			"KONSOLE_DBUS_SESSION": "/Sessions/2", "KONSOLE_VERSION": "260803"},
		installed: []string{"konsole"},
		bus:       bus,
	}
}

func TestKonsoleTabAndSplit(t *testing.T) {
	bus := &fakeKonsole{sessions: []int{1, 2}, hier: "(0)[0]\n(1)[1]"}
	f := konsoleEnv(bus)
	l := f.launcher(LaunchOptions{SSHUser: "me"})

	res, err := l.SSH(peer, ModeTab)
	if err != nil || res.Where != "new Konsole tab" || bus.commands[3] != "ssh me@100.64.0.9" {
		t.Fatalf("tab: res %+v err %v commands %v", res, err, bus.commands)
	}
	res, err = l.SSH(peer, ModeSplitDown)
	if err != nil || res.Where != "Konsole split" || bus.commands[4] != "ssh me@100.64.0.9" {
		t.Fatalf("split: res %+v err %v commands %v", res, err, bus.commands)
	}
	if want := []string{"1v"}; !reflect.DeepEqual(bus.splits, want) {
		t.Fatalf("split view/orientation %v, want %v (session 2 is view 1)", bus.splits, want)
	}
}

func TestKonsoleLockedFallsBackToWindow(t *testing.T) {
	bus := &fakeKonsole{locked: true, sessions: []int{1, 2}, hier: "(0)[0|1]"}
	f := konsoleEnv(bus)
	res, err := f.launcher(LaunchOptions{}).SSH(peer, ModeSplitRight)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "security sensitive") || len(bus.splits) != 0 {
		t.Fatalf("note %q splits %v", res.Note, bus.splits)
	}
	if want := []string{"konsole", "-e", "ssh", "100.64.0.9"}; !reflect.DeepEqual(f.started[0], want) {
		t.Fatalf("started %v", f.started)
	}
}

func TestAvailable(t *testing.T) {
	locked := konsoleEnv(&fakeKonsole{locked: true, sessions: []int{1}})
	l := locked.launcher(LaunchOptions{})
	if err := l.Available(ModeWindow); err != nil {
		t.Fatalf("window: %v", err)
	}
	if err := l.Available(ModeSplitRight); err == nil || !strings.Contains(err.Error(), "security sensitive") {
		t.Fatalf("locked Konsole split: %v", err)
	}
	open := konsoleEnv(&fakeKonsole{sessions: []int{1}})
	if err := open.launcher(LaunchOptions{}).Available(ModeTab); err != nil {
		t.Fatalf("open Konsole tab: %v", err)
	}
	tmux := &fakeEnv{env: map[string]string{"TMUX": "x"}}
	if err := tmux.launcher(LaunchOptions{}).Available(ModeSplitDown); err != nil {
		t.Fatalf("tmux: %v", err)
	}
	none := &fakeEnv{env: map[string]string{}}
	if err := none.launcher(LaunchOptions{}).Available(ModeTab); err == nil {
		t.Fatal("no terminal: want error")
	}
	if err := none.launcher(LaunchOptions{}).Available(ModeHere); err != nil {
		t.Fatalf("here: %v", err)
	}
}

func TestViewIDs(t *testing.T) {
	if got := viewIDs("(0)[0|(2)[1|3]]\n(1)[4]"); !reflect.DeepEqual(got, []int{0, 1, 3, 4}) {
		t.Fatalf("got %v", got)
	}
}

func TestRDP(t *testing.T) {
	// krdc is preferred and starts on its own.
	f := &fakeEnv{env: map[string]string{}, installed: []string{"krdc", "xfreerdp3"}}
	l := f.launcher(LaunchOptions{})
	if _, err := l.RDP(peer); err != nil || !reflect.DeepEqual(f.started[0], []string{"krdc", "rdp://100.64.0.9"}) {
		t.Fatalf("started %v err %v", f.started, err)
	}
	// FreeRDP runs in a terminal, with the user when set.
	f = &fakeEnv{env: map[string]string{}, installed: []string{"xfreerdp3", "xterm"}}
	l = f.launcher(LaunchOptions{RDPUser: "Administrator"})
	if _, err := l.RDP(peer); err != nil {
		t.Fatal(err)
	}
	want := []string{"xterm", "-e", "xfreerdp3", "/v:100.64.0.9", "/u:Administrator", "/dynamic-resolution", "/cert:tofu"}
	if !reflect.DeepEqual(f.started[0], want) {
		t.Fatalf("started %v", f.started)
	}
	// A template drops {user} arguments without a user.
	l = (&fakeEnv{}).launcher(LaunchOptions{RDPCommand: []string{"rdp", "{host}", "--user={user}", "{name}"}})
	if argv, _, _ := l.RDPArgv(peer); !reflect.DeepEqual(argv, []string{"rdp", "100.64.0.9", "web"}) {
		t.Fatalf("argv %v", argv)
	}
	if _, err := (&fakeEnv{}).launcher(LaunchOptions{}).RDP(peer); err == nil {
		t.Fatal("want error without RDP client")
	}
}

func TestShellJoin(t *testing.T) {
	if got := ShellJoin([]string{"ssh", "-o", "ProxyCommand=nc %h %p", "it's"}); got != `ssh -o 'ProxyCommand=nc %h %p' 'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}

func TestParseMode(t *testing.T) {
	if m, err := ParseMode("split-right"); err != nil || m != ModeSplitRight {
		t.Fatal(m, err)
	}
	if _, err := ParseMode("sideways"); err == nil {
		t.Fatal("want error")
	}
}
