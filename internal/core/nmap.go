package core

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// runTool runs an external program and returns its standard output; a
// failure carries the program's own message.
func runTool(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return out, fmt.Errorf("%s is not installed", bin)
		}
		if ctx.Err() != nil {
			return out, fmt.Errorf("%s: %w", bin, ctx.Err())
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%s: %s", bin, strings.Join(strings.Fields(msg), " "))
		}
		return out, fmt.Errorf("%s: %w", bin, err)
	}
	return out, nil
}

// ScanArgs are the nmap arguments for a scan of ip; the XML report goes
// to standard output.
func (s *Service) ScanArgs(ip string, full bool) []string {
	args := s.opts.QuickScan
	if full {
		args = s.opts.FullScan
	}
	out := append([]string{"-oX", "-"}, args...)
	return append(out, ip)
}

// Scan runs nmap against a peer's NetBird IP.
func (s *Service) Scan(ctx context.Context, ip string, full bool, now time.Time) Scan {
	args := s.ScanArgs(ip, full)
	sc := Scan{IP: ip, Full: full, Args: args, StartedAt: now}
	start := time.Now()
	out, err := s.runNmap(ctx, args...)
	sc.FetchedAt = now.Add(time.Since(start))
	if err != nil && len(out) == 0 {
		sc.Err = err
		return sc
	}
	if perr := parseNmap(out, &sc); perr != nil {
		sc.Err = errors.Join(err, perr)
		return sc
	}
	sc.Err = err
	return sc
}

type nmapRun struct {
	Args  string `xml:"args,attr"`
	Hosts []struct {
		Status struct {
			State string `xml:"state,attr"`
		} `xml:"status"`
		Ports struct {
			Extra []struct {
				State string `xml:"state,attr"`
				Count int    `xml:"count,attr"`
			} `xml:"extraports"`
			Ports []struct {
				Protocol string `xml:"protocol,attr"`
				PortID   int    `xml:"portid,attr"`
				State    struct {
					State string `xml:"state,attr"`
				} `xml:"state"`
				Service struct {
					Name      string `xml:"name,attr"`
					Product   string `xml:"product,attr"`
					Version   string `xml:"version,attr"`
					ExtraInfo string `xml:"extrainfo,attr"`
				} `xml:"service"`
			} `xml:"port"`
		} `xml:"ports"`
	} `xml:"host"`
	RunStats struct {
		Finished struct {
			Elapsed string `xml:"elapsed,attr"`
			Summary string `xml:"summary,attr"`
			ErrMsg  string `xml:"errormsg,attr"`
		} `xml:"finished"`
	} `xml:"runstats"`
}

func parseNmap(data []byte, sc *Scan) error {
	var r nmapRun
	if err := xml.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("parsing nmap output: %w", err)
	}
	if secs, err := strconv.ParseFloat(r.RunStats.Finished.Elapsed, 64); err == nil {
		sc.Elapsed = time.Duration(secs * float64(time.Second))
	}
	sc.Summary = r.RunStats.Finished.Summary
	if msg := r.RunStats.Finished.ErrMsg; msg != "" {
		return fmt.Errorf("nmap: %s", msg)
	}
	for _, h := range r.Hosts {
		sc.HostUp = sc.HostUp || h.Status.State == "up"
		for _, e := range h.Ports.Extra {
			sc.Closed += e.Count
		}
		for _, p := range h.Ports.Ports {
			sc.Ports = append(sc.Ports, Port{
				Protocol: p.Protocol,
				Number:   p.PortID,
				State:    p.State.State,
				Service:  p.Service.Name,
				Product:  p.Service.Product,
				Version:  p.Service.Version,
				Extra:    p.Service.ExtraInfo,
			})
		}
	}
	return nil
}
