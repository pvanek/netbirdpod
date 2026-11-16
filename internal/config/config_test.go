package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvanek/netbirdpod/internal/core"
)

func TestMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Found || cfg.Refresh != time.Minute || cfg.Launch.SSHMode != "" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{
		"refresh": "30s",
		"ssh_user": "admin",
		"ssh_mode": "split-right",
		"rdp_mode": "tab",
		"nmap_quick": ["-Pn", "-F"],
		"peers": {"web": {"ssh_user": "root", "host": "web.lan"}}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Found || cfg.Refresh != 30*time.Second || cfg.Launch.SSHMode != core.ModeSplitRight ||
		cfg.Launch.RDPMode != core.ModeTab || cfg.Launch.SSHUser != "admin" || len(cfg.Service.QuickScan) != 2 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if p := cfg.Launch.Peers["web"]; p.SSHUser != "root" || p.Host != "web.lan" {
		t.Fatalf("peer settings: %+v", p)
	}
}

func TestInvalid(t *testing.T) {
	for _, data := range []string{`{"ssh_mode": "sideways"}`, `{"refresh": "1s"}`, `{"refresh": "soon"}`, `{`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s: want error", data)
		}
	}
}
