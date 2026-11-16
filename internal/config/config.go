// Package config loads the optional settings file
// ~/.config/netbirdpod/config.json (or $NETBIRDPOD_CONFIG). netbirdpod
// works without one: NetBird itself is configured through its own client.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pvanek/netbirdpod/internal/core"
)

// DefaultRefresh is how often the visible screen reloads.
const DefaultRefresh = time.Minute

// File is the on-disk configuration. Every field is optional.
type File struct {
	Netbird string `json:"netbird,omitempty"` // netbird binary
	Nmap    string `json:"nmap,omitempty"`    // nmap binary
	// Refresh is the auto-refresh interval, e.g. "1m" or "30s".
	Refresh string `json:"refresh,omitempty"`

	SSHCommand []string `json:"ssh_command,omitempty"` // e.g. ["ssh", "-A"] or ["netbird", "ssh"]
	SSHUser    string   `json:"ssh_user,omitempty"`
	// SSHMode is where "s" opens ssh: here, window, tab, split-right or
	// split-down.
	SSHMode string `json:"ssh_mode,omitempty"`
	// UseFQDN connects to NetBird FQDNs instead of IPs (needs NetBird DNS).
	UseFQDN  bool     `json:"use_fqdn,omitempty"`
	Terminal []string `json:"terminal,omitempty"` // new-window command, e.g. ["konsole", "-e"]

	RDPCommand []string `json:"rdp_command,omitempty"` // template: {host}, {user}, {name}
	RDPUser    string   `json:"rdp_user,omitempty"`
	RDPMode    string   `json:"rdp_mode,omitempty"` // where FreeRDP opens

	NmapQuick []string `json:"nmap_quick,omitempty"`
	NmapFull  []string `json:"nmap_full,omitempty"`

	// Peers holds per-peer settings by peer name, FQDN or IP.
	Peers map[string]PeerEntry `json:"peers,omitempty"`
}

type PeerEntry struct {
	SSHUser string `json:"ssh_user,omitempty"`
	RDPUser string `json:"rdp_user,omitempty"`
	Host    string `json:"host,omitempty"`
}

// Config is the validated configuration.
type Config struct {
	Path    string
	Found   bool // the file exists
	Refresh time.Duration
	Service core.Options
	Launch  core.LaunchOptions
}

// DefaultPath is $NETBIRDPOD_CONFIG or ~/.config/netbirdpod/config.json.
func DefaultPath() (string, error) {
	if p := os.Getenv("NETBIRDPOD_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "netbirdpod", "config.json"), nil
}

// Load reads and validates the configuration at path; a missing file
// gives the defaults.
func Load(path string) (*Config, error) {
	var f File
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) || path == "":
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	cfg, err := f.resolve()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	cfg.Found = data != nil
	return cfg, nil
}

func (f File) resolve() (*Config, error) {
	cfg := &Config{
		Refresh: DefaultRefresh,
		Service: core.Options{NetbirdBin: f.Netbird, NmapBin: f.Nmap, QuickScan: f.NmapQuick, FullScan: f.NmapFull},
		Launch: core.LaunchOptions{
			SSHCommand: f.SSHCommand,
			SSHUser:    f.SSHUser,
			UseFQDN:    f.UseFQDN,
			Terminal:   f.Terminal,
			RDPCommand: f.RDPCommand,
			RDPUser:    f.RDPUser,
		},
	}
	if f.Refresh != "" {
		d, err := time.ParseDuration(f.Refresh)
		if err != nil || d < 5*time.Second {
			return nil, fmt.Errorf("refresh %q: want a duration of at least 5s, e.g. \"1m\"", f.Refresh)
		}
		cfg.Refresh = d
	}
	var err error
	if f.SSHMode != "" {
		if cfg.Launch.SSHMode, err = core.ParseMode(f.SSHMode); err != nil {
			return nil, fmt.Errorf("ssh_mode: %w", err)
		}
	}
	if f.RDPMode != "" {
		if cfg.Launch.RDPMode, err = core.ParseMode(f.RDPMode); err != nil {
			return nil, fmt.Errorf("rdp_mode: %w", err)
		}
	}
	if len(f.Peers) > 0 {
		cfg.Launch.Peers = make(map[string]core.PeerSettings, len(f.Peers))
		for name, p := range f.Peers {
			cfg.Launch.Peers[name] = core.PeerSettings(p)
		}
	}
	return cfg, nil
}
