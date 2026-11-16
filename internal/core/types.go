// Package core is the UI-agnostic service over the local NetBird client
// and the external tools netbirdpod drives (nmap, ssh, RDP clients and
// terminals). It exposes plain domain types.
package core

import (
	"strings"
	"time"
)

// Daemon states reported by "netbird status".
const (
	DaemonConnected      = "Connected"
	DaemonConnecting     = "Connecting"
	DaemonIdle           = "Idle"
	DaemonNeedsLogin     = "NeedsLogin"
	DaemonLoginFailed    = "LoginFailed"
	DaemonSessionExpired = "SessionExpired"
)

// Status is a snapshot of the local NetBird client.
type Status struct {
	FetchedAt time.Time
	Err       error

	Daemon  string // daemon state, e.g. Connected, Idle, NeedsLogin
	Profile string // active profile

	FQDN, IP, IPv6  string
	PublicKey       string
	KernelInterface bool
	WireguardPort   int

	CLIVersion, DaemonVersion string

	Management, Signal Endpoint
	Relays             []Relay
	Networks           []string // routes this peer uses
	DNS                []DNSGroup
	Events             []Event

	LazyConnection              bool
	QuantumResistance           bool
	QuantumResistancePermissive bool
	SSHServer                   bool
	SSHSessions                 int
	SessionExpiresAt            time.Time

	Peers          []Peer
	PeersConnected int
}

// Connected reports whether the daemon is connected to the network.
func (s Status) Connected() bool { return s.Daemon == DaemonConnected }

// Up reports whether the daemon is connected or connecting.
func (s Status) Up() bool { return s.Daemon == DaemonConnected || s.Daemon == DaemonConnecting }

// NeedsLogin reports whether connecting needs an (SSO) login first.
func (s Status) NeedsLogin() bool {
	switch s.Daemon {
	case DaemonNeedsLogin, DaemonLoginFailed, DaemonSessionExpired:
		return true
	}
	return false
}

// RelaysAvailable counts the reachable relays and STUN/TURN servers.
func (s Status) RelaysAvailable() int {
	n := 0
	for _, r := range s.Relays {
		if r.Available {
			n++
		}
	}
	return n
}

// Endpoint is the management or signal service.
type Endpoint struct {
	URL       string
	Connected bool
	Error     string
}

type Relay struct {
	URI       string
	Available bool
	Transport string
	Error     string
}

// DNSGroup is a group of nameservers and the domains they serve.
type DNSGroup struct {
	Servers []string
	Domains []string
	Enabled bool
	Error   string
}

type Event struct {
	ID       string
	Time     time.Time
	Severity string
	Category string
	Message  string
}

// Peer is a remote peer this client may reach.
type Peer struct {
	FQDN      string
	IP, IPv6  string
	PublicKey string
	// Status is Connected, Connecting or Idle (lazy connections stay idle
	// until traffic flows).
	Status         string
	ConnType       string // P2P or Relayed
	LocalICE       string // ICE candidate types: host, srflx, prflx, relay
	RemoteICE      string
	LocalEndpoint  string
	RemoteEndpoint string
	Relay          string
	StatusChanged  time.Time
	LastHandshake  time.Time
	Received, Sent int64 // bytes
	Latency        time.Duration
	Quantum        bool
	Networks       []string // routes the peer offers
}

// Name is the peer's host name: the first label of its FQDN.
func (p Peer) Name() string {
	if name, _, ok := strings.Cut(p.FQDN, "."); ok && name != "" {
		return name
	}
	if p.FQDN != "" {
		return p.FQDN
	}
	return p.IP
}

// Connected reports whether a tunnel to the peer is up.
func (p Peer) Connected() bool { return p.Status == DaemonConnected }

// Key identifies the peer across refreshes.
func (p Peer) Key() string { return p.PublicKey }

// Profile is a NetBird client profile: one account / management server.
type Profile struct {
	Name   string
	Active bool
	Email  string // account, from the user's state file; may be empty
}

type ProfileList struct {
	FetchedAt time.Time
	Err       error
	Profiles  []Profile
}

// Active returns the active profile.
func (l ProfileList) Active() (Profile, bool) {
	for _, p := range l.Profiles {
		if p.Active {
			return p, true
		}
	}
	return Profile{}, false
}

// Scan is the result of an nmap scan of one peer.
type Scan struct {
	IP        string
	Full      bool // service and version detection
	Args      []string
	StartedAt time.Time
	FetchedAt time.Time // when it finished
	Elapsed   time.Duration
	HostUp    bool
	Ports     []Port
	// Closed counts ports that answered but are closed or filtered, which
	// nmap summarises instead of listing.
	Closed  int
	Summary string
	Err     error
}

// Open returns the open ports.
func (s Scan) Open() []Port {
	var out []Port
	for _, p := range s.Ports {
		if p.State == "open" {
			out = append(out, p)
		}
	}
	return out
}

// HasOpen reports whether the TCP port is open.
func (s Scan) HasOpen(port int) bool {
	for _, p := range s.Ports {
		if p.Number == port && p.Protocol == "tcp" && p.State == "open" {
			return true
		}
	}
	return false
}

type Port struct {
	Protocol string
	Number   int
	State    string // open, filtered, closed, open|filtered
	Service  string
	Product  string
	Version  string
	Extra    string
}

// Description is the product, version and extra info nmap detected.
func (p Port) Description() string {
	return strings.Join(strings.Fields(p.Product+" "+p.Version+" "+p.Extra), " ")
}
