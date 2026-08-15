package main

// The per-session watch daemon: subscribes to herdr's socket event stream
// (including pane.updated, which herdr refuses to deliver to plugin [[events]]
// hooks) and turns events into debounced reconcile passes. This is what makes
// an agent's title change — Claude's /rename — show up in the tab instantly,
// and what lets the manifest keep only a minimal watchdog hook set.
//
// Lifecycle: herdr's [[startup]] spawns `watch`, which gates on config and
// re-execs itself as `watch --detached` (setsid, /dev/null stdio) so herdr's
// wait() returns and no in-flight plugin slot stays pinned. The detached
// daemon holds a per-session flock for its lifetime — which doubles as the
// liveness probe the watchdog hooks use to revive it — and exits when the
// event stream ends (server gone; the next server's startup respawns it).
//
// CPU discipline: high-frequency events take cheap paths (title-only pass,
// or a targeted tab rename fed from the event payload — zero subprocesses),
// full passes are debounced AND rate-floored, and env files are stat-polled
// on a slow tick.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type triggerKind int

const (
	triggerFull triggerKind = iota
	triggerTitle
	triggerRename
	triggerEnv
)

type paneEvent struct {
	PaneID string
	TabID  string
	Agent  string
	Title  string
}

type trigger struct {
	kind triggerKind
	pane paneEvent
}

type watchOps struct {
	full   func()
	title  func(bypassEnvCache bool)
	rename func(paneEvent)
}

type watchTimings struct {
	Debounce     time.Duration
	FullFloor    time.Duration
	StatInterval time.Duration
	ReadDeadline time.Duration
	BinaryPoll   time.Duration
}

func defaultWatchTimings() watchTimings {
	return watchTimings{
		Debounce:     250 * time.Millisecond,
		FullFloor:    2 * time.Second,
		StatInterval: 5 * time.Second,
		ReadDeadline: 10 * time.Minute,
		BinaryPoll:   15 * time.Second,
	}
}

// watchSubscriptions are the daemon's event feed. pane.updated fires only on
// stripped-title changes (herdr filters at the source); the rest mirror what
// the manifest hooks used to subscribe before the daemon took them over.
var watchSubscriptions = []string{
	"pane.updated",
	"workspace.focused", "workspace.renamed",
	"tab.created", "tab.focused", "tab.renamed",
	"pane.created", "pane.closed", "pane.exited", "pane.moved", "pane.focused",
	"pane.agent_detected",
	// NOT pane.agent_status_changed: its socket subscription is per-pane
	// (requires pane_id), so the manifest hook keeps delivering it and runs a
	// cheap title-only pass while the daemon is alive.
}

// classifyEvent maps one event line to a trigger, tracking last stripped
// titles per pane so only real changes fire. Without terminalTitles,
// non-agent pane title changes are dropped outright — nothing downstream
// would use them. A title cleared to "" is recorded but fires nothing — the
// targeted rename path has no name to apply. Unknown/garbage lines are nil.
func classifyEvent(line []byte, lastTitles map[string]string, terminalTitles bool) *trigger {
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			Pane *struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
				Agent  string `json:"agent"`
				Title  string `json:"terminal_title_stripped"`
			} `json:"pane"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &ev); err != nil || ev.Event == "" {
		return nil
	}
	switch ev.Event {
	case "pane_updated":
		p := ev.Data.Pane
		if p == nil || (!terminalTitles && p.Agent == "") {
			return nil
		}
		if lastTitles[p.PaneID] == p.Title {
			return nil
		}
		lastTitles[p.PaneID] = p.Title
		if p.Title == "" {
			return nil
		}
		return &trigger{kind: triggerRename, pane: paneEvent{
			PaneID: p.PaneID, TabID: p.TabID, Agent: p.Agent, Title: p.Title,
		}}
	case "pane_agent_status_changed":
		return &trigger{kind: triggerTitle}
	default:
		return &trigger{kind: triggerFull}
	}
}

// runScheduler drains triggers into debounced, scoped work. A full pass
// absorbs all pending cheap work; full passes additionally respect a rate
// floor so no event storm can exceed a known cost ceiling. Returns when the
// triggers channel closes or stop is closed — the daemon uses stop, because
// closing a channel that statWatcher/binaryWatcher may still send into is a
// race (and a panic under unlucky timing).
func runScheduler(triggers <-chan trigger, ops watchOps, timings watchTimings, stop <-chan struct{}) {
	var (
		pendingFull, pendingTitle, pendingBypass bool
		renames                                  = map[string]paneEvent{}
		lastFull                                 time.Time
	)
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	timerArmed := false
	arm := func(d time.Duration) {
		if timerArmed && !timer.Stop() {
			<-timer.C
		}
		timer.Reset(d)
		timerArmed = true
	}

	fire := func() {
		timerArmed = false
		if pendingFull {
			if wait := timings.FullFloor - time.Since(lastFull); wait > 0 {
				// Floored: run the cheap work now, keep the full pending.
				for _, p := range renames {
					ops.rename(p)
				}
				renames = map[string]paneEvent{}
				if pendingTitle {
					ops.title(pendingBypass)
					pendingTitle, pendingBypass = false, false
				}
				arm(wait)
				return
			}
			ops.full()
			lastFull = time.Now()
			pendingFull, pendingTitle, pendingBypass = false, false, false
			renames = map[string]paneEvent{}
			return
		}
		for _, p := range renames {
			ops.rename(p)
		}
		renames = map[string]paneEvent{}
		if pendingTitle {
			ops.title(pendingBypass)
			pendingTitle, pendingBypass = false, false
		}
	}

	for {
		select {
		case <-stop:
			return
		case tr, ok := <-triggers:
			if !ok {
				return
			}
			switch tr.kind {
			case triggerFull:
				pendingFull = true
			case triggerTitle:
				pendingTitle = true
			case triggerEnv:
				pendingTitle = true
				pendingBypass = true
			case triggerRename:
				renames[tr.pane.PaneID] = tr.pane
				pendingTitle = true // a rename can change the focused ${tab}
			}
			arm(timings.Debounce)
		case <-timer.C:
			fire()
		}
	}
}

// pingSocket dials a fresh connection and asks the server for a ping.
func pingSocket(sockPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", sockPath, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := fmt.Fprintf(conn, `{"id":"ping","method":"ping","params":{}}`+"\n"); err != nil {
		return false
	}
	_, err = bufio.NewReader(conn).ReadString('\n')
	return err == nil
}

// flockNB takes a nonblocking exclusive lock on an open file.
func flockNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// watchLockPath names the daemon's per-session liveness lock.
func watchLockPath(stateDir, session string) string {
	return filepath.Join(stateDir, "watch.lock."+session)
}

// restartSelf replaces the running daemon with the binary at path — the
// same PID execs the freshly installed (or freshly rebuilt) image, and Go's
// CLOEXEC fds release the singleton flock atomically at exec so the new
// image re-acquires it without a race. If the exec fails (binary gone:
// uninstall), the daemon simply dies; watchdog hooks stop reviving it once
// the plugin is unregistered. A var so tests can stub the exec. The announce
// marker makes the NEW image toast the handoff — announcing before exec
// would hold the old daemon alive through the retry loop.
var restartSelf = func(exePath string) {
	_ = syscall.Exec(exePath, []string{exePath, "watch", "--detached"}, withAnnounceEnv(os.Environ()))
	os.Exit(0)
}

// announceEnvVar marks an exec-handoff so the replacement daemon announces
// itself; a fresh daemon (startup, watchdog revival) stays silent.
const announceEnvVar = "HWT_ANNOUNCE_RESTART"

// withAnnounceEnv appends the announce marker to an environment, once.
func withAnnounceEnv(environ []string) []string {
	for _, kv := range environ {
		if strings.HasPrefix(kv, announceEnvVar+"=") {
			return environ
		}
	}
	return append(environ, announceEnvVar+"=1")
}

// pluginVersion reads the plugin version from the manifest two levels above
// the binary (<root>/bin/herdr-titles -> <root>/herdr-plugin.toml). At
// announce time the installed checkout is already the NEW version — exactly
// the one worth naming. Empty when unreadable.
func pluginVersion(exePath string) string {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exePath)), "herdr-plugin.toml"))
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// announceRestart toasts the daemon handoff via notification.show, retrying
// while herdr refuses with "busy" (the user is mid-keystroke; toasts deliver
// on input-idle). "disabled" means toasts are off in the herdr config — never
// retry that. Transport errors retry too: the server may still be settling
// after the plugin update. Returns whether the toast was shown.
func announceRestart(sockPath, body string, interval time.Duration, maxAttempts int) bool {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(interval)
		}
		result, err := apiRequest(sockPath, "notification.show", map[string]string{
			"title": "herdr-titles",
			"body":  body,
		})
		if err != nil {
			continue
		}
		var status struct {
			Shown  bool   `json:"shown"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(result, &status) != nil {
			return false
		}
		if status.Shown {
			return true
		}
		if status.Reason == "disabled" {
			return false
		}
	}
	return false
}

// binaryIdentity is the executable's change-detection fingerprint.
type binaryIdentity struct {
	modTime time.Time
	size    int64
}

// fingerprintBinary stats the executable for change detection; nil when it
// cannot be fingerprinted right now.
func fingerprintBinary(exePath string) *binaryIdentity {
	info, err := os.Stat(exePath)
	if os.Getenv("HWT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "DEBUG fingerprint exe=%q statErr=%v\n", exePath, err)
	}
	if err != nil {
		return nil
	}
	return &binaryIdentity{info.ModTime(), info.Size()}
}

// binaryWatcher polls the daemon's own executable and hands the process over
// to a replaced binary. The baseline is captured by the CALLER before the
// event subscription, so a replacement that lands at any point after startup
// is always detected. A single stat error is tolerated (installs replace the
// checkout with a brief rename gap); two consecutive misses mean the plugin
// is gone and the handoff doubles as an orderly exit. A nil baseline (binary
// unreadable at startup) is filled in by the first successful poll.
func binaryWatcher(exePath string, baseline *binaryIdentity, interval time.Duration, stop <-chan struct{}) {
	misses := 0
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			info, err := os.Stat(exePath)
			if err != nil {
				if misses++; misses >= 2 {
					restartSelf(exePath)
					return
				}
				continue
			}
			misses = 0
			id := binaryIdentity{info.ModTime(), info.Size()}
			if baseline == nil {
				baseline = &id
				continue
			}
			if id != *baseline {
				if os.Getenv("HWT_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "DEBUG binaryWatcher change: %v -> %v, restarting\n", *baseline, id)
				}
				restartSelf(exePath)
				return
			}
		}
	}
}

// watchDaemon runs the daemon without binary self-restart (tests).
func watchDaemon(sockPath, stateDir, session string, watchFiles []string, ops watchOps, timings watchTimings) error {
	return watchDaemonAt(sockPath, stateDir, session, "", true, watchFiles, ops, timings)
}

// watchDaemonAt is the detached daemon body: singleton lock, subscribe, then
// pump events into the scheduler until the stream ends. watchFiles are
// stat-polled for env changes; binPath (when non-empty) is the daemon's own
// executable, watched so a plugin update restarts the daemon onto the new
// binary automatically; terminalTitles gates non-agent rename triggers
// (classifyEvent). Returns nil on every orderly exit — a held lock or a
// dead server are normal, not errors.
func watchDaemonAt(sockPath, stateDir, session, binPath string, terminalTitles bool, watchFiles []string, ops watchOps, timings watchTimings) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(watchLockPath(stateDir, session), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := flockNB(lockFile); err != nil {
		return nil // another daemon is alive; that's the desired state
	}

	// Fingerprint the binary BEFORE subscribing: anything that replaces it
	// after this point — however quickly — is detected as a change.
	var baseline *binaryIdentity
	if binPath != "" {
		baseline = fingerprintBinary(binPath)
	}

	conn, err := net.DialTimeout("unix", sockPath, socketTimeout)
	if err != nil {
		return nil // server not up; a watchdog will retry later
	}
	defer conn.Close()

	subs := make([]string, 0, len(watchSubscriptions))
	for _, s := range watchSubscriptions {
		subs = append(subs, fmt.Sprintf(`{"type":%q}`, s))
	}
	payload := fmt.Sprintf(`{"id":"watch","method":"events.subscribe","params":{"subscriptions":[%s]}}`,
		joinComma(subs))
	if _, err := fmt.Fprintf(conn, "%s\n", payload); err != nil {
		return nil
	}
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(timings.ReadDeadline))
	if _, err := reader.ReadString('\n'); err != nil {
		return nil // no subscription confirmation
	}

	triggers := make(chan trigger, 64)
	schedStop := make(chan struct{})
	schedDone := make(chan struct{})
	go func() { runScheduler(triggers, ops, timings, schedStop); close(schedDone) }()

	stopStat := make(chan struct{})
	if len(watchFiles) > 0 {
		go statWatcher(watchFiles, timings.StatInterval, triggers, stopStat)
	}
	if binPath != "" {
		go binaryWatcher(binPath, baseline, timings.BinaryPoll, stopStat)
	}

	lastTitles := map[string]string{}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(timings.ReadDeadline))
		line, err := reader.ReadString('\n')
		if err != nil {
			if isTimeout(err) && pingSocket(sockPath, socketTimeout) {
				continue // quiet session, server alive: keep streaming
			}
			break // EOF or dead server: exit; watchdogs revive us
		}
		if tr := classifyEvent([]byte(line), lastTitles, terminalTitles); tr != nil {
			select {
			case triggers <- *tr:
			default: // scheduler saturated; drop — passes are idempotent
			}
		}
	}

	close(stopStat)
	close(schedStop)
	<-schedDone
	return nil
}

// statWatcher polls watched files' mtimes and raises env triggers on change.
func statWatcher(files []string, interval time.Duration, triggers chan<- trigger, stop <-chan struct{}) {
	seen := map[string]time.Time{}
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			seen[f] = info.ModTime()
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			for _, f := range files {
				info, err := os.Stat(f)
				if err != nil {
					continue
				}
				if mt := info.ModTime(); mt != seen[f] {
					seen[f] = mt
					select {
					case triggers <- trigger{kind: triggerEnv}:
					default:
					}
				}
			}
		}
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// runWatchParent is what herdr's [[startup]] spawns: gate on environment and
// config, then hand off to a detached daemon so herdr's wait() returns.
func runWatchParent() error {
	if os.Getenv("HERDR_SOCKET_PATH") == "" {
		return nil // not spawned by a herdr server
	}
	cfg, err := LoadConfig(filepath.Join(pluginConfigDir(), "config.hcl"))
	if err != nil {
		return err
	}
	if !cfg.Tabs.WatchTitles {
		return nil // daemon disabled; watchdog hooks still run full passes
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(self, "watch", "--detached")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// runWatchDetached wires the daemon to the real reconcile passes.
func runWatchDetached() error {
	sockPath := os.Getenv("HERDR_SOCKET_PATH")
	session := envOr("HERDR_SESSION", "default")
	if sockPath == "" {
		return nil
	}
	cfg, err := LoadConfig(filepath.Join(pluginConfigDir(), "config.hcl"))
	if err != nil {
		return err
	}
	stateDir := pluginStateDir()
	exePath, _ := os.Executable()
	if os.Getenv(announceEnvVar) != "" {
		// This image replaced a running daemon (binary update): toast the
		// handoff. Unset so a future watchdog respawn stays silent; the
		// goroutine retries past "busy" while the daemon gets on with its job.
		os.Unsetenv(announceEnvVar)
		body := "Daemon restarted onto a new binary"
		if v := pluginVersion(exePath); v != "" {
			body = "Updated to " + v + " — daemon restarted"
		}
		go announceRestart(sockPath, body, 2*time.Second, 150)
	}
	ops := watchOps{
		full: func() { _ = run("watch.event") },
		title: func(bypass bool) {
			_ = runTitleOnly("watch.title", bypass)
		},
		rename: func(p paneEvent) {
			_ = withLock(stateDir, session, func() error {
				return RenameTabForTitle(
					sockPath,
					tabStatePath(stateDir, session),
					p.TabID, p.Agent, p.Title, cfg.Tabs)
			})
		},
	}
	return watchDaemonAt(sockPath, stateDir, session, exePath, cfg.Tabs.TerminalTitles, cfg.EnvWatchFiles, ops, defaultWatchTimings())
}

// daemonAlive probes the daemon's liveness lock: if we can take it, nobody
// holds it — the daemon is dead. Microseconds, no I/O beyond open+flock.
func daemonAlive(stateDir, session string) bool {
	f, err := os.OpenFile(watchLockPath(stateDir, session), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := flockNB(f); err != nil {
		return true // held: a daemon is alive
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// spawnDaemon launches the watch parent (which gates on config and
// daemonizes). A var so tests can stub the exec.
var spawnDaemon = func() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "watch")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	_ = cmd.Start()
	go func() { _ = cmd.Wait() }() // reap the short-lived parent
}

// ensureDaemon revives a dead daemon. Races between concurrent revivers are
// settled by the daemon's own singleton lock.
func ensureDaemon(stateDir, session string) {
	if os.Getenv("HERDR_SOCKET_PATH") == "" {
		return // nothing to watch without a server socket
	}
	if !daemonAlive(stateDir, session) {
		spawnDaemon()
	}
}

// runStatusHook handles pane.agent_status_changed: the one event the daemon
// cannot subscribe to globally. With a live daemon it still does the cheap
// work (attention counts changed -> title-only pass); with a dead daemon it
// doubles as a watchdog like the others.
func runStatusHook(event string) error {
	stateDir := pluginStateDir()
	session := envOr("HERDR_SESSION", "default")
	if daemonAlive(stateDir, session) {
		return runTitleOnly(event, false)
	}
	ensureDaemon(stateDir, session)
	return run(event)
}

// runWatchdog handles the manifest's retained event hooks: with a live
// daemon they are no-ops (the daemon already saw the event); with a dead one
// they revive it and run the reconcile inline to cover the gap.
func runWatchdog(event string) error {
	stateDir := pluginStateDir()
	session := envOr("HERDR_SESSION", "default")
	if daemonAlive(stateDir, session) {
		return nil
	}
	ensureDaemon(stateDir, session)
	return run(event)
}
