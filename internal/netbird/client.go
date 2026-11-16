// Package netbird drives the local NetBird client through its CLI. The CLI
// talks to the daemon over its socket and is the stable interface of a
// NetBird installation, so netbirdpod needs no access to the daemon itself.
package netbird

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner runs the netbird binary with args and returns its standard output.
// A failed run returns an error carrying the command's own message.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ExecRunner runs bin (usually "netbird") as a child process.
func ExecRunner(bin string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return out, commandError(args, out, stderr.Bytes(), err)
		}
		return out, nil
	}
}

// commandError names the subcommand and keeps the CLI's own explanation,
// which is more useful than "exit status 1".
func commandError(args []string, stdout, stderr []byte, err error) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(stdout))
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("netbird CLI not found: %w", err)
	}
	sub := "netbird"
	if len(args) > 0 {
		sub += " " + args[0]
	}
	if msg == "" {
		return fmt.Errorf("%s: %w", sub, err)
	}
	// The CLI prefixes errors with "Error: " and adds usage on bad flags.
	msg, _, _ = strings.Cut(msg, "\nUsage:")
	msg = strings.TrimPrefix(strings.TrimSpace(msg), "Error: ")
	return fmt.Errorf("%s: %s", sub, oneLine(msg))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// Client is the NetBird CLI.
type Client struct {
	Bin string
	run Runner
	// stateDir holds the per-profile state files of the user
	// (~/.config/netbird); empty disables reading them.
	stateDir string
}

// New returns a client running bin; run may be nil to use ExecRunner.
func New(bin string, run Runner) *Client {
	if bin == "" {
		bin = "netbird"
	}
	if run == nil {
		run = ExecRunner(bin)
	}
	c := &Client{Bin: bin, run: run}
	if dir, err := os.UserConfigDir(); err == nil {
		c.stateDir = filepath.Join(dir, "netbird")
	}
	return c
}

// SetStateDir overrides where the per-profile state files are read from.
func (c *Client) SetStateDir(dir string) { c.stateDir = dir }

// Status returns the detailed status of the daemon and its peers.
func (c *Client) Status(ctx context.Context) (*StatusJSON, error) {
	out, err := c.run(ctx, "status", "--json")
	if err != nil {
		return nil, err
	}
	return ParseStatus(out)
}

// ParseStatus parses the output of "netbird status --json".
func ParseStatus(data []byte) (*StatusJSON, error) {
	var s StatusJSON
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing netbird status: %w", err)
	}
	return &s, nil
}

// Profiles lists the profiles of the current user.
func (c *Client) Profiles(ctx context.Context) ([]ProfileEntry, error) {
	out, err := c.run(ctx, "profile", "list")
	if err != nil {
		return nil, err
	}
	return ParseProfiles(out), nil
}

// ParseProfiles parses the table of "netbird profile list":
//
//	NAME     ACTIVE
//	default  ✓
//	work
func ParseProfiles(data []byte) []ProfileEntry {
	var out []ProfileEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || (fields[0] == "NAME" && len(out) == 0) {
			continue
		}
		p := ProfileEntry{Name: fields[0]}
		for _, f := range fields[1:] {
			if strings.Contains(f, "✓") || strings.EqualFold(f, "yes") || strings.EqualFold(f, "true") {
				p.Active = true
			}
		}
		out = append(out, p)
	}
	return out
}

// ProfileEmail is the account e-mail stored in the user's state file of
// a profile, if any.
func (c *Client) ProfileEmail(name string) string {
	if c.stateDir == "" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(c.stateDir, name+".state.json"))
	if err != nil {
		return ""
	}
	var st struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(data, &st) != nil {
		return ""
	}
	return st.Email
}

// Down disconnects from the NetBird network.
func (c *Client) Down(ctx context.Context) error {
	_, err := c.run(ctx, "down")
	return err
}

// SelectProfile makes name the active profile.
func (c *Client) SelectProfile(ctx context.Context, name string) error {
	_, err := c.run(ctx, "profile", "select", name)
	return err
}

// UpArgs are the arguments of "netbird up" for profile (empty: the active
// one). Up may need an interactive SSO login, so it is run in the
// terminal rather than through the Runner.
func UpArgs(profile string) []string {
	if profile == "" {
		return []string{"up"}
	}
	return []string{"up", "--profile", profile}
}

// LoginArgs are the arguments of "netbird login" for profile.
func LoginArgs(profile string) []string {
	if profile == "" {
		return []string{"login"}
	}
	return []string{"login", "--profile", profile}
}
