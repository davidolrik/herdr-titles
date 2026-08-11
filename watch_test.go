package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestClassifyEvent(t *testing.T) {
	last := map[string]string{}
	paneEv := func(agent, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","tab_id":"w1:t1","agent":%q,"terminal_title_stripped":%q}}}`,
			agent, title)
	}

	if tr := classifyEvent([]byte(paneEv("claude", "First")), last); tr == nil || tr.kind != triggerRename || tr.pane.Title != "First" {
		t.Fatalf("new agent title => %+v, want rename trigger", tr)
	}
	if tr := classifyEvent([]byte(paneEv("claude", "First")), last); tr != nil {
		t.Errorf("unchanged title => %+v, want nil", tr)
	}
	if tr := classifyEvent([]byte(paneEv("claude", "Second")), last); tr == nil || tr.kind != triggerRename {
		t.Errorf("changed title => %+v, want rename", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "shell title")), last); tr != nil {
		t.Errorf("non-agent pane => %+v, want nil (hooks own shell panes)", tr)
	}
	if tr := classifyEvent([]byte(`{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed"}}`), last); tr == nil || tr.kind != triggerTitle {
		t.Errorf("status change => %+v, want title-only", tr)
	}
	if tr := classifyEvent([]byte(`{"event":"tab_focused","data":{"type":"tab_focused"}}`), last); tr == nil || tr.kind != triggerFull {
		t.Errorf("tab focus => %+v, want full", tr)
	}
	if tr := classifyEvent([]byte(`not json`), last); tr != nil {
		t.Errorf("garbage => %+v, want nil", tr)
	}
}

type opsRecorder struct {
	mu      sync.Mutex
	fulls   int
	titles  []bool // bypass flags
	renames []string
}

func (r *opsRecorder) ops() watchOps {
	return watchOps{
		full: func() { r.mu.Lock(); r.fulls++; r.mu.Unlock() },
		title: func(bypass bool) {
			r.mu.Lock()
			r.titles = append(r.titles, bypass)
			r.mu.Unlock()
		},
		rename: func(p paneEvent) {
			r.mu.Lock()
			r.renames = append(r.renames, p.TabID+"="+p.Title)
			r.mu.Unlock()
		},
	}
}

func (r *opsRecorder) snapshot() (int, []bool, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fulls, append([]bool{}, r.titles...), append([]string{}, r.renames...)
}

func testTimings() watchTimings {
	return watchTimings{
		Debounce:     20 * time.Millisecond,
		FullFloor:    150 * time.Millisecond,
		StatInterval: 10 * time.Millisecond,
		ReadDeadline: 500 * time.Millisecond,
	}
}

func TestSchedulerCoalescesAndScopes(t *testing.T) {
	rec := &opsRecorder{}
	triggers := make(chan trigger, 16)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { runScheduler(triggers, rec.ops(), testTimings()); close(done) }()

	// A burst of title triggers plus one rename coalesces into one of each.
	triggers <- trigger{kind: triggerTitle}
	triggers <- trigger{kind: triggerTitle}
	triggers <- trigger{kind: triggerRename, pane: paneEvent{PaneID: "p", TabID: "t1", Agent: "claude", Title: "A"}}
	triggers <- trigger{kind: triggerTitle}
	time.Sleep(80 * time.Millisecond)

	fulls, titles, renames := rec.snapshot()
	if fulls != 0 || len(titles) != 1 || len(renames) != 1 || renames[0] != "t1=A" {
		t.Fatalf("burst => fulls=%d titles=%v renames=%v, want 0/1/1", fulls, titles, renames)
	}

	// A full trigger absorbs pending cheap work.
	triggers <- trigger{kind: triggerTitle}
	triggers <- trigger{kind: triggerFull}
	time.Sleep(80 * time.Millisecond)
	fulls, titles, _ = rec.snapshot()
	if fulls != 1 || len(titles) != 1 {
		t.Fatalf("full absorb => fulls=%d titles=%v, want 1 full, no extra title", fulls, titles)
	}

	// Rate floor: an immediate second full waits out the floor.
	triggers <- trigger{kind: triggerFull}
	time.Sleep(60 * time.Millisecond) // < floor remaining
	fulls, _, _ = rec.snapshot()
	if fulls != 1 {
		t.Fatalf("floored full ran early: fulls=%d", fulls)
	}
	time.Sleep(200 * time.Millisecond) // floor expires
	fulls, _, _ = rec.snapshot()
	if fulls != 2 {
		t.Fatalf("floored full never ran: fulls=%d", fulls)
	}

	// Env change rides the title path with a cache bypass.
	triggers <- trigger{kind: triggerEnv}
	time.Sleep(80 * time.Millisecond)
	_, titles, _ = rec.snapshot()
	if len(titles) == 0 || !titles[len(titles)-1] {
		t.Fatalf("env change => titles=%v, want trailing bypass=true", titles)
	}

	close(stop)
	close(triggers)
	<-done
}

// fakeEventServer accepts one subscription connection, records the subscribe
// request, and lets the test feed event lines. Fresh connections get a ping
// response when pingOK.
type fakeEventServer struct {
	t        *testing.T
	ln       net.Listener
	sockPath string
	subGot   chan string
	events   chan string
	closeSub chan struct{}
	pingOK   bool
}

func newFakeEventServer(t *testing.T, pingOK bool) *fakeEventServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "hwtw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s := &fakeEventServer{
		t:        t,
		sockPath: filepath.Join(dir, "herdr.sock"),
		subGot:   make(chan string, 1),
		events:   make(chan string, 16),
		closeSub: make(chan struct{}),
		pingOK:   pingOK,
	}
	s.ln, err = net.Listen("unix", s.sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ln.Close() })
	go s.serve()
	return s
}

func (s *fakeEventServer) serve() {
	first := true
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			conn.Close()
			continue
		}
		if strings.Contains(line, "events.subscribe") && first {
			first = false
			s.subGot <- line
			conn.Write([]byte(`{"id":"watch","result":{"type":"subscription_started"}}` + "\n"))
			go func(c net.Conn) {
				defer c.Close()
				for {
					select {
					case ev, ok := <-s.events:
						if !ok {
							return
						}
						if _, err := c.Write([]byte(ev + "\n")); err != nil {
							return
						}
					case <-s.closeSub:
						return
					}
				}
			}(conn)
			continue
		}
		if strings.Contains(line, `"ping"`) && s.pingOK {
			conn.Write([]byte(`{"id":"ping","result":{"type":"pong"}}` + "\n"))
		}
		conn.Close()
	}
}

func TestWatchDaemonLoop(t *testing.T) {
	srv := newFakeEventServer(t, true)
	rec := &opsRecorder{}
	stateDir := t.TempDir()

	done := make(chan error, 1)
	go func() {
		done <- watchDaemon(srv.sockPath, stateDir, "wtest", nil, rec.ops(), testTimings())
	}()

	select {
	case sub := <-srv.subGot:
		for _, want := range []string{"pane.updated", "tab.focused", "pane.agent_detected"} {
			if !strings.Contains(sub, want) {
				t.Errorf("subscribe payload missing %s: %s", want, sub)
			}
		}
		if strings.Contains(sub, "pane.agent_status_changed") {
			t.Error("subscribed to pane.agent_status_changed, which requires a pane_id")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon never subscribed")
	}

	srv.events <- `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","tab_id":"w1:t1","agent":"claude","terminal_title_stripped":"Renamed"}}}`
	time.Sleep(100 * time.Millisecond)
	_, _, renames := rec.snapshot()
	if len(renames) != 1 || renames[0] != "w1:t1=Renamed" {
		t.Fatalf("renames = %v, want [w1:t1=Renamed]", renames)
	}

	// EOF ends the daemon cleanly.
	close(srv.closeSub)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exit error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit on EOF")
	}
}

func TestWatchDaemonSingleton(t *testing.T) {
	srv := newFakeEventServer(t, true)
	stateDir := t.TempDir()

	// Hold the watch lock the way a live daemon would.
	holder, err := os.OpenFile(filepath.Join(stateDir, "watch.lock.wtest"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := flockNB(holder); err != nil {
		t.Fatal(err)
	}

	if err := watchDaemon(srv.sockPath, stateDir, "wtest", nil, (&opsRecorder{}).ops(), testTimings()); err != nil {
		t.Fatalf("second daemon errored: %v", err)
	}
	select {
	case sub := <-srv.subGot:
		t.Fatalf("second daemon subscribed despite held lock: %s", sub)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestWatchDaemonEnvFileTrigger(t *testing.T) {
	srv := newFakeEventServer(t, true)
	rec := &opsRecorder{}
	stateDir := t.TempDir()
	envFile := filepath.Join(stateDir, "watched.env")
	if err := os.WriteFile(envFile, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- watchDaemon(srv.sockPath, stateDir, "wtest", []string{envFile}, rec.ops(), testTimings())
	}()
	<-srv.subGot

	time.Sleep(50 * time.Millisecond)
	past := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(envFile, past, past); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, titles, _ := rec.snapshot()
		if len(titles) > 0 && titles[len(titles)-1] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("env file change never triggered a bypass title pass")
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(srv.closeSub)
	<-done
}

func TestWatchDaemonPingKeepsAliveThenDeadExits(t *testing.T) {
	srv := newFakeEventServer(t, true)
	rec := &opsRecorder{}
	stateDir := t.TempDir()
	timings := testTimings()
	timings.ReadDeadline = 100 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		done <- watchDaemon(srv.sockPath, stateDir, "wtest", nil, rec.ops(), timings)
	}()
	<-srv.subGot

	// Silence for several deadlines: ping succeeds, daemon stays.
	select {
	case err := <-done:
		t.Fatalf("daemon exited during ping-alive silence: %v", err)
	case <-time.After(400 * time.Millisecond):
	}

	// Kill the server entirely: next deadline's ping fails -> exit.
	srv.ln.Close()
	close(srv.closeSub)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit after server death")
	}
}

var _ = json.Marshal // placate imports during TDD scaffolding

func TestDaemonAliveAndWatchdog(t *testing.T) {
	stateDir := t.TempDir()
	if daemonAlive(stateDir, "s") {
		t.Fatal("no daemon, but reported alive")
	}
	holder, err := os.OpenFile(watchLockPath(stateDir, "s"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := flockNB(holder); err != nil {
		t.Fatal(err)
	}
	if !daemonAlive(stateDir, "s") {
		t.Fatal("lock held, but reported dead")
	}

	// Watchdog with a live daemon: exits without reconciling or spawning.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	t.Setenv("HERDR_SESSION", "s")
	t.Setenv("HERDR_SOCKET_PATH", "/nonexistent.sock")
	t.Setenv("HERDR_BIN_PATH", "/nonexistent-herdr")
	spawned := 0
	orig := spawnDaemon
	spawnDaemon = func() { spawned++ }
	defer func() { spawnDaemon = orig }()

	if err := runWatchdog("tab.focused"); err != nil {
		t.Fatalf("watchdog with live daemon errored: %v", err)
	}
	if spawned != 0 {
		t.Error("watchdog spawned despite live daemon")
	}

	// Dead daemon: revive + inline reconcile (which fails on the fake herdr
	// path — that error must surface, proving the reconcile ran).
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	err = runWatchdog("tab.focused")
	if spawned != 1 {
		t.Errorf("dead daemon not revived: spawned=%d", spawned)
	}
	if err == nil {
		t.Error("inline reconcile did not run (expected snapshot failure)")
	}
}
