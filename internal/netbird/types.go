package netbird

import (
	"encoding/json"
	"time"
)

// StatusJSON is the output of "netbird status --json". Fields not used by
// netbirdpod are left out.
type StatusJSON struct {
	Peers struct {
		Total     int        `json:"total"`
		Connected int        `json:"connected"`
		Details   []PeerJSON `json:"details"`
	} `json:"peers"`
	CLIVersion    string      `json:"cliVersion"`
	DaemonVersion string      `json:"daemonVersion"`
	DaemonStatus  string      `json:"daemonStatus"`
	Management    ServiceJSON `json:"management"`
	Signal        ServiceJSON `json:"signal"`
	Relays        struct {
		Total     int         `json:"total"`
		Available int         `json:"available"`
		Details   []RelayJSON `json:"details"`
	} `json:"relays"`
	NetbirdIP                   string          `json:"netbirdIp"`
	NetbirdIPv6                 string          `json:"netbirdIpv6"`
	PublicKey                   string          `json:"publicKey"`
	UsesKernelInterface         bool            `json:"usesKernelInterface"`
	WireguardPort               int             `json:"wireguardPort"`
	FQDN                        string          `json:"fqdn"`
	QuantumResistance           bool            `json:"quantumResistance"`
	QuantumResistancePermissive bool            `json:"quantumResistancePermissive"`
	Networks                    []string        `json:"networks"`
	DNSServers                  []DNSServerJSON `json:"dnsServers"`
	Events                      []EventJSON     `json:"events"`
	LazyConnectionEnabled       bool            `json:"lazyConnectionEnabled"`
	ProfileName                 string          `json:"profileName"`
	SSHServer                   struct {
		Enabled  bool              `json:"enabled"`
		Sessions []json.RawMessage `json:"sessions"`
	} `json:"sshServer"`
	SessionExpiresAt time.Time `json:"sessionExpiresAt"`
}

type ServiceJSON struct {
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	Error     string `json:"error"`
}

type RelayJSON struct {
	URI       string `json:"uri"`
	Available bool   `json:"available"`
	Error     string `json:"error"`
	Transport string `json:"transport"`
}

type DNSServerJSON struct {
	Servers []string `json:"servers"`
	Domains []string `json:"domains"`
	Enabled bool     `json:"enabled"`
	Error   string   `json:"error"`
}

type EventJSON struct {
	ID          string            `json:"id"`
	Severity    string            `json:"severity"`
	Category    string            `json:"category"`
	Message     string            `json:"message"`
	UserMessage string            `json:"userMessage"`
	Timestamp   time.Time         `json:"timestamp"`
	Metadata    map[string]string `json:"metadata"`
}

type PeerJSON struct {
	FQDN             string    `json:"fqdn"`
	NetbirdIP        string    `json:"netbirdIp"`
	NetbirdIPv6      string    `json:"netbirdIpv6"`
	PublicKey        string    `json:"publicKey"`
	Status           string    `json:"status"`
	LastStatusUpdate time.Time `json:"lastStatusUpdate"`
	ConnectionType   string    `json:"connectionType"`
	ICECandidateType struct {
		Local  string `json:"local"`
		Remote string `json:"remote"`
	} `json:"iceCandidateType"`
	ICECandidateEndpoint struct {
		Local  string `json:"local"`
		Remote string `json:"remote"`
	} `json:"iceCandidateEndpoint"`
	RelayAddress           string    `json:"relayAddress"`
	LastWireguardHandshake time.Time `json:"lastWireguardHandshake"`
	TransferReceived       int64     `json:"transferReceived"`
	TransferSent           int64     `json:"transferSent"`
	// Latency is a Go time.Duration, in nanoseconds.
	Latency           int64    `json:"latency"`
	QuantumResistance bool     `json:"quantumResistance"`
	Networks          []string `json:"networks"`
}

// ProfileEntry is one line of "netbird profile list".
type ProfileEntry struct {
	Name   string
	Active bool
}
