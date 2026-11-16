package netbird

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseProfiles(t *testing.T) {
	out := []byte("NAME     ACTIVE\ndefault  ✓\nwork\n\n")
	got := ParseProfiles(out)
	want := []ProfileEntry{{Name: "default", Active: true}, {Name: "work"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseStatus(t *testing.T) {
	data := []byte(`{"peers":{"total":1,"connected":1,"details":[{"fqdn":"a.nb.example.com","netbirdIp":"100.64.0.2",
		"status":"Connected","connectionType":"P2P","latency":12000000,"lastWireguardHandshake":"2026-11-16T07:00:00Z",
		"transferReceived":2048,"networks":["10.0.0.0/24"]}]},
		"daemonStatus":"Connected","management":{"url":"https://api.example.com:443","connected":true},
		"netbirdIp":"100.64.0.1/16","fqdn":"me.nb.example.com","profileName":"default",
		"dnsServers":[{"servers":["8.8.8.8:53"],"domains":["example.com"],"enabled":true}],
		"sshServer":{"enabled":true,"sessions":[{}]}}`)
	s, err := ParseStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	if s.DaemonStatus != "Connected" || s.Peers.Details[0].Latency != 12_000_000 || s.DNSServers[0].Servers[0] != "8.8.8.8:53" ||
		len(s.SSHServer.Sessions) != 1 || s.Management.URL != "https://api.example.com:443" {
		t.Fatalf("unexpected parse: %+v", s)
	}
}

func TestClientUsesRunner(t *testing.T) {
	var calls [][]string
	c := New("nb", func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "down" {
			return nil, errors.New("netbird down: daemon not running")
		}
		return []byte("NAME ACTIVE\nwork ✓\n"), nil
	})
	if err := c.Down(context.Background()); err == nil {
		t.Fatal("want error from down")
	}
	if p, err := c.Profiles(context.Background()); err != nil || len(p) != 1 || !p[0].Active {
		t.Fatalf("profiles: %+v %v", p, err)
	}
	if err := c.SelectProfile(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"down"}, {"profile", "list"}, {"profile", "select", "work"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
}

func TestProfileEmail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "work.state.json"), []byte(`{"email":"me@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New("", nil)
	c.SetStateDir(dir)
	if got := c.ProfileEmail("work"); got != "me@example.com" {
		t.Fatalf("got %q", got)
	}
	if got := c.ProfileEmail("missing"); got != "" {
		t.Fatalf("got %q for a missing profile", got)
	}
	if got := c.ProfileEmail("../work"); got != "" {
		t.Fatalf("path in profile name was followed: %q", got)
	}
}

func TestCommandError(t *testing.T) {
	err := commandError([]string{"up"}, nil, []byte("Error: daemon is not running\nUsage:\n  netbird up"), errors.New("exit status 1"))
	if got := err.Error(); got != "netbird up: daemon is not running" {
		t.Fatalf("got %q", got)
	}
}
