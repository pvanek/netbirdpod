package core

import (
	"context"
	"os/exec"
	"sort"
	"time"

	"github.com/pvanek/netbirdpod/internal/netbird"
)

// Default nmap arguments: a quick scan of the 100 most common ports, and a
// full one of the top 1000 with service and version detection. -Pn skips
// host discovery, which peers that drop ICMP would fail.
var (
	DefaultQuickScan = []string{"-Pn", "-T4", "-F"}
	DefaultFullScan  = []string{"-Pn", "-T4", "-sV", "--top-ports", "1000"}
)

// Options configures the service.
type Options struct {
	NetbirdBin string   // default "netbird"
	NmapBin    string   // default "nmap"
	QuickScan  []string // default DefaultQuickScan
	FullScan   []string // default DefaultFullScan
}

// Service reads the local NetBird client and scans peers.
type Service struct {
	nb   *netbird.Client
	opts Options
	// runNmap runs nmap; replaced in tests.
	runNmap func(ctx context.Context, args ...string) ([]byte, error)
}

func NewService(opts Options, nb *netbird.Client) *Service {
	if opts.NmapBin == "" {
		opts.NmapBin = "nmap"
	}
	if len(opts.QuickScan) == 0 {
		opts.QuickScan = DefaultQuickScan
	}
	if len(opts.FullScan) == 0 {
		opts.FullScan = DefaultFullScan
	}
	if nb == nil {
		nb = netbird.New(opts.NetbirdBin, nil)
	}
	s := &Service{nb: nb, opts: opts}
	s.runNmap = func(ctx context.Context, args ...string) ([]byte, error) {
		return runTool(ctx, opts.NmapBin, args...)
	}
	return s
}

// Status reads the client's status and peers.
func (s *Service) Status(ctx context.Context, now time.Time) Status {
	st := Status{FetchedAt: now}
	raw, err := s.nb.Status(ctx)
	if err != nil {
		st.Err = err
		return st
	}
	return convertStatus(raw, now)
}

func convertStatus(r *netbird.StatusJSON, now time.Time) Status {
	st := Status{
		FetchedAt:                   now,
		Daemon:                      r.DaemonStatus,
		Profile:                     r.ProfileName,
		FQDN:                        r.FQDN,
		IP:                          r.NetbirdIP,
		IPv6:                        r.NetbirdIPv6,
		PublicKey:                   r.PublicKey,
		KernelInterface:             r.UsesKernelInterface,
		WireguardPort:               r.WireguardPort,
		CLIVersion:                  r.CLIVersion,
		DaemonVersion:               r.DaemonVersion,
		Management:                  Endpoint(r.Management),
		Signal:                      Endpoint(r.Signal),
		Networks:                    r.Networks,
		LazyConnection:              r.LazyConnectionEnabled,
		QuantumResistance:           r.QuantumResistance,
		QuantumResistancePermissive: r.QuantumResistancePermissive,
		SSHServer:                   r.SSHServer.Enabled,
		SSHSessions:                 len(r.SSHServer.Sessions),
		SessionExpiresAt:            realTime(r.SessionExpiresAt),
		PeersConnected:              r.Peers.Connected,
	}
	for _, rl := range r.Relays.Details {
		st.Relays = append(st.Relays, Relay{URI: rl.URI, Available: rl.Available, Transport: rl.Transport, Error: rl.Error})
	}
	for _, d := range r.DNSServers {
		st.DNS = append(st.DNS, DNSGroup(d))
	}
	for _, e := range r.Events {
		msg := e.UserMessage
		if msg == "" {
			msg = e.Message
		}
		st.Events = append(st.Events, Event{ID: e.ID, Time: realTime(e.Timestamp), Severity: e.Severity, Category: e.Category, Message: msg})
	}
	sort.SliceStable(st.Events, func(a, b int) bool { return st.Events[a].Time.After(st.Events[b].Time) })
	for _, p := range r.Peers.Details {
		st.Peers = append(st.Peers, Peer{
			FQDN:           p.FQDN,
			IP:             p.NetbirdIP,
			IPv6:           p.NetbirdIPv6,
			PublicKey:      p.PublicKey,
			Status:         p.Status,
			ConnType:       dashEmpty(p.ConnectionType),
			LocalICE:       p.ICECandidateType.Local,
			RemoteICE:      p.ICECandidateType.Remote,
			LocalEndpoint:  p.ICECandidateEndpoint.Local,
			RemoteEndpoint: p.ICECandidateEndpoint.Remote,
			Relay:          p.RelayAddress,
			StatusChanged:  realTime(p.LastStatusUpdate),
			LastHandshake:  realTime(p.LastWireguardHandshake),
			Received:       p.TransferReceived,
			Sent:           p.TransferSent,
			Latency:        time.Duration(p.Latency),
			Quantum:        p.QuantumResistance,
			Networks:       p.Networks,
		})
	}
	sort.SliceStable(st.Peers, func(a, b int) bool { return st.Peers[a].FQDN < st.Peers[b].FQDN })
	return st
}

// realTime maps NetBird's "never" (Go's zero time, sometimes rendered in
// a local offset such as 0001-01-01T00:57:44+00:57) to the zero time.
func realTime(t time.Time) time.Time {
	if t.Year() <= 1 {
		return time.Time{}
	}
	return t
}

func dashEmpty(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

// Profiles lists the client profiles of the current user.
func (s *Service) Profiles(ctx context.Context, now time.Time) ProfileList {
	l := ProfileList{FetchedAt: now}
	entries, err := s.nb.Profiles(ctx)
	if err != nil {
		l.Err = err
		return l
	}
	for _, e := range entries {
		l.Profiles = append(l.Profiles, Profile{Name: e.Name, Active: e.Active, Email: s.nb.ProfileEmail(e.Name)})
	}
	return l
}

// Down disconnects.
func (s *Service) Down(ctx context.Context) error { return s.nb.Down(ctx) }

// SelectProfile makes name the active profile.
func (s *Service) SelectProfile(ctx context.Context, name string) error {
	return s.nb.SelectProfile(ctx, name)
}

// UpCommand connects using profile. It may run an SSO login, so it is
// meant to run in the terminal.
func (s *Service) UpCommand(profile string) *exec.Cmd {
	return exec.Command(s.nb.Bin, netbird.UpArgs(profile)...)
}

// LoginCommand logs profile in. It is meant to run in the terminal.
func (s *Service) LoginCommand(profile string) *exec.Cmd {
	return exec.Command(s.nb.Bin, netbird.LoginArgs(profile)...)
}
