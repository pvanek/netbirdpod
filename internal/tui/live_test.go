package tui

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pvanek/netbirdpod/internal/core"
	"github.com/pvanek/netbirdpod/internal/netbird"
)

// TestLiveRender prints every screen with the real NetBird client; run
// with NETBIRDPOD_LIVE=1 go test ./internal/tui -run Live -v.
func TestLiveRender(t *testing.T) {
	if os.Getenv("NETBIRDPOD_LIVE") == "" {
		t.Skip("set NETBIRDPOD_LIVE=1 to render with the local NetBird client")
	}
	svc := core.NewService(core.Options{}, netbird.New("", nil))
	var tm tea.Model = New(svc, core.NewLauncher(core.LaunchOptions{}), time.Minute)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	ctx := context.Background()
	tm, _ = tm.Update(loadedMsg[core.Status]{data: svc.Status(ctx, time.Now())})
	tm, _ = tm.Update(loadedMsg[core.ProfileList]{data: svc.Profiles(ctx, time.Now())})
	for _, k := range []string{"0", "1", "2"} {
		tm, _ = tm.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		fmt.Println(ansi.Strip(tm.(Model).render()))
		fmt.Println("=================")
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fmt.Println(ansi.Strip(tm.(Model).render()))
}

// TestLiveScan scans this machine's own NetBird IP.
func TestLiveScan(t *testing.T) {
	if os.Getenv("NETBIRDPOD_LIVE") == "" {
		t.Skip("set NETBIRDPOD_LIVE=1 to scan with the local nmap")
	}
	svc := core.NewService(core.Options{}, netbird.New("", nil))
	st := svc.Status(context.Background(), time.Now())
	if st.Err != nil {
		t.Fatal(st.Err)
	}
	sc := svc.Scan(context.Background(), stripMask(st.IP), false, time.Now())
	if sc.Err != nil {
		t.Fatal(sc.Err)
	}
	fmt.Printf("%+v\n", sc)
}
