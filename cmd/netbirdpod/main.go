// Command netbirdpod is a terminal console for the local NetBird client:
// profiles, this peer, and the peers it can reach, with ssh, RDP and nmap.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pvanek/netbirdpod/internal/config"
	"github.com/pvanek/netbirdpod/internal/core"
	"github.com/pvanek/netbirdpod/internal/netbird"
	"github.com/pvanek/netbirdpod/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: netbirdpod [flags] [command]

Commands:
  (none)    start the console
  status    print this peer and its peers once and exit

Flags:
`

func main() {
	defaultPath, _ := config.DefaultPath()
	configPath := flag.String("config", defaultPath, "configuration file (optional)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("netbirdpod", version)
		return
	}
	if err := run(*configPath, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "netbirdpod:", err)
		os.Exit(1)
	}
}

func run(configPath, command string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	svc := core.NewService(cfg.Service, netbird.New(cfg.Service.NetbirdBin, nil))

	switch command {
	case "":
		launch := core.NewLauncher(cfg.Launch)
		_, err = tea.NewProgram(tui.New(svc, launch, cfg.Refresh)).Run()
		return err
	case "status":
		return printStatus(svc)
	default:
		flag.Usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

// printStatus prints the connection and a table of peers.
func printStatus(svc *core.Service) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now()
	st := svc.Status(ctx, now)
	if st.Err != nil {
		return st.Err
	}
	fmt.Printf("Profile:    %s\nStatus:     %s\nThis peer:  %s %s\nManagement: %s\n\n",
		st.Profile, st.Daemon, st.FQDN, st.IP, st.Management.URL)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PEER\tIP\tSTATUS\tTYPE\tLATENCY\tROUTES")
	for _, p := range st.Peers {
		lat := "-"
		if p.Latency > 0 {
			lat = p.Latency.Round(time.Millisecond).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name(), p.IP, p.Status, dash(p.ConnType), lat, dash(strings.Join(p.Networks, ",")))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\n%d of %d peers connected\n", st.PeersConnected, len(st.Peers))
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
