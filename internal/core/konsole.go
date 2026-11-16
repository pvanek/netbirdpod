package core

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Konsole is driven over D-Bus: Window.newSession opens a tab and
// Window.createSplit a pane next to a view, and Session.runCommand types
// the command into the new shell. Konsole guards runCommand (and sendText)
// behind "Enable the security sensitive parts of the D-Bus API" (Settings
// → Configure Konsole → General); without it a tab or pane would open but
// stay empty, so netbirdpod checks first and opens a window instead.
//
// Konsole sets KONSOLE_DBUS_SERVICE, KONSOLE_DBUS_WINDOW and
// KONSOLE_DBUS_SESSION in every session, so the tab or pane goes into the
// window netbirdpod runs in.

// KonsoleBus is the part of Konsole's D-Bus API netbirdpod uses.
type KonsoleBus interface {
	// CanRunCommand reports whether runCommand is allowed, probing with an
	// empty sendText to the given (own) session.
	CanRunCommand(session string) (bool, error)
	NewSession(window string) (int, error)
	SessionList(window string) ([]int, error)
	ViewHierarchy(window string) (string, error)
	CreateSplit(window string, view int, horizontal bool) error
	RunCommand(session int, command string) error
	SetTabTitle(session int, title string) error
	Close() error
}

const errAccessDenied = "org.freedesktop.DBus.Error.AccessDenied"

// errKonsoleLocked explains how to enable command injection.
var errKonsoleLocked = errors.New(`Konsole blocks running commands over D-Bus; enable "security sensitive parts of the D-Bus API" in Configure Konsole → General`)

// konsoleBus connects to the Konsole netbirdpod runs in and checks that it
// allows running commands; the caller closes the bus.
func (l *Launcher) konsoleBus() (KonsoleBus, error) {
	service, window, session := l.getenv("KONSOLE_DBUS_SERVICE"), l.getenv("KONSOLE_DBUS_WINDOW"), l.getenv("KONSOLE_DBUS_SESSION")
	if window == "" || session == "" {
		return nil, errors.New("Konsole D-Bus window or session unknown")
	}
	bus, err := l.konsole(service)
	if err != nil {
		return nil, fmt.Errorf("Konsole D-Bus: %w", err)
	}
	ok, err := bus.CanRunCommand(session)
	if err == nil && !ok {
		err = errKonsoleLocked
	} else if err != nil {
		err = fmt.Errorf("Konsole D-Bus: %w", err)
	}
	if err != nil {
		bus.Close()
		return nil, err
	}
	return bus, nil
}

// konsoleCheck reports whether Konsole lets netbirdpod run commands.
func (l *Launcher) konsoleCheck() error {
	bus, err := l.konsoleBus()
	if err != nil {
		return err
	}
	return bus.Close()
}

func (l *Launcher) konsoleOpen(argv []string, title string, mode Mode) (string, error) {
	window, session := l.getenv("KONSOLE_DBUS_WINDOW"), l.getenv("KONSOLE_DBUS_SESSION")
	bus, err := l.konsoleBus()
	if err != nil {
		return "", err
	}
	defer bus.Close()

	var id int
	where := "new Konsole tab"
	switch mode {
	case ModeTab:
		if id, err = bus.NewSession(window); err != nil {
			return "", fmt.Errorf("Konsole newSession: %w", err)
		}
	case ModeSplitRight, ModeSplitDown:
		where = "Konsole split"
		if id, err = konsoleSplit(bus, window, session, mode == ModeSplitRight); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("Konsole cannot open %s", mode)
	}
	_ = bus.SetTabTitle(id, title) // cosmetic
	if err := bus.RunCommand(id, ShellJoin(argv)); err != nil {
		return "", fmt.Errorf("Konsole runCommand: %w", err)
	}
	return where, nil
}

// konsoleSplit splits the view of netbirdpod's own session and returns the
// new pane's session. horizontal puts the panes side by side.
func konsoleSplit(bus KonsoleBus, window, session string, horizontal bool) (int, error) {
	own, err := sessionID(session)
	if err != nil {
		return 0, err
	}
	hier, err := bus.ViewHierarchy(window)
	if err != nil {
		return 0, fmt.Errorf("Konsole viewHierarchy: %w", err)
	}
	// Konsole numbers sessions from 1 and views (terminal displays) from 0,
	// one each per new tab or pane, so a session's view is its id minus
	// one unless tabs were moved between windows.
	view := own - 1
	if !slices.Contains(viewIDs(hier), view) {
		return 0, fmt.Errorf("cannot find the Konsole view of session %d", own)
	}
	before, err := bus.SessionList(window)
	if err != nil {
		return 0, fmt.Errorf("Konsole sessionList: %w", err)
	}
	if err := bus.CreateSplit(window, view, horizontal); err != nil {
		return 0, fmt.Errorf("Konsole createSplit: %w", err)
	}
	after, err := bus.SessionList(window)
	if err != nil {
		return 0, fmt.Errorf("Konsole sessionList: %w", err)
	}
	for _, id := range after {
		if !slices.Contains(before, id) {
			return id, nil
		}
	}
	return 0, errors.New("Konsole created no new session for the split")
}

// sessionID parses "/Sessions/3".
func sessionID(path string) (int, error) {
	id, err := strconv.Atoi(path[strings.LastIndex(path, "/")+1:])
	if err != nil {
		return 0, fmt.Errorf("bad Konsole session %q", path)
	}
	return id, nil
}

var viewRe = regexp.MustCompile(`[\[|](\d+)`)

// viewIDs extracts the views of a viewHierarchy such as "(0)[0|1]",
// one line per tab; "(n)" are splitters.
func viewIDs(hier string) []int {
	var out []int
	for _, m := range viewRe.FindAllStringSubmatch(hier, -1) {
		if id, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// dbusKonsole implements KonsoleBus on the session bus.
type dbusKonsole struct {
	conn    *dbus.Conn
	service string
}

func dialKonsole(service string) (KonsoleBus, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return &dbusKonsole{conn: conn, service: service}, nil
}

func (k *dbusKonsole) Close() error { return k.conn.Close() }

func (k *dbusKonsole) call(path, method string, out any, args ...any) error {
	call := k.conn.Object(k.service, dbus.ObjectPath(path)).Call(method, 0, args...)
	if call.Err != nil {
		return call.Err
	}
	if out != nil {
		return call.Store(out)
	}
	return nil
}

func (k *dbusKonsole) CanRunCommand(session string) (bool, error) {
	err := k.call(session, "org.kde.konsole.Session.sendText", nil, "")
	var de dbus.Error
	if errors.As(err, &de) && de.Name == errAccessDenied {
		return false, nil
	}
	return err == nil, err
}

func (k *dbusKonsole) NewSession(window string) (int, error) {
	var id int32
	err := k.call(window, "org.kde.konsole.Window.newSession", &id)
	return int(id), err
}

func (k *dbusKonsole) SessionList(window string) ([]int, error) {
	var ids []string
	if err := k.call(window, "org.kde.konsole.Window.sessionList", &ids); err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ids))
	for _, s := range ids {
		if id, err := strconv.Atoi(s); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

func (k *dbusKonsole) ViewHierarchy(window string) (string, error) {
	var lines []string
	err := k.call(window, "org.kde.konsole.Window.viewHierarchy", &lines)
	return strings.Join(lines, "\n"), err
}

func (k *dbusKonsole) CreateSplit(window string, view int, horizontal bool) error {
	var ok bool
	if err := k.call(window, "org.kde.konsole.Window.createSplit", &ok, int32(view), horizontal); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("view %d not split", view)
	}
	return nil
}

func (k *dbusKonsole) RunCommand(session int, command string) error {
	return k.call(fmt.Sprintf("/Sessions/%d", session), "org.kde.konsole.Session.runCommand", nil, command)
}

func (k *dbusKonsole) SetTabTitle(session int, title string) error {
	path := fmt.Sprintf("/Sessions/%d", session)
	// Contexts 0 and 1 are the local and remote (ssh) tab title formats.
	for _, ctx := range []int32{0, 1} {
		if err := k.call(path, "org.kde.konsole.Session.setTabTitleFormat", nil, ctx, title); err != nil {
			return err
		}
	}
	return nil
}
