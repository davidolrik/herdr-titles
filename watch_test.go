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
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestClassifyEvent(t *testing.T) {
	st := newClassifyState()
	paneEv := func(agent, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","tab_id":"w1:t1","agent":%q,"terminal_title_stripped":%q}}}`,
			agent, title)
	}

	if tr := classifyEvent([]byte(paneEv("claude", "First")), st, true); tr == nil || tr.kind != triggerRename || tr.pane.Title != "First" {
		t.Fatalf("new agent title => %+v, want rename trigger", tr)
	}
	if tr := classifyEvent([]byte(paneEv("claude", "First")), st, true); tr != nil {
		t.Errorf("unchanged title => %+v, want nil", tr)
	}
	if tr := classifyEvent([]byte(paneEv("claude", "Second")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("changed title => %+v, want rename", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "shell title")), st, false); tr != nil {
		t.Errorf("non-agent title without terminal_titles => %+v, want nil", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "shell title")), st, true); tr == nil || tr.kind != triggerRename || tr.pane.Agent != "" {
		t.Errorf("non-agent title => %+v, want rename trigger with empty agent", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "shell title")), st, true); tr != nil {
		t.Errorf("unchanged non-agent title => %+v, want nil", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "")), st, true); tr == nil || tr.kind != triggerRename || tr.pane.Title != "" {
		t.Errorf("cleared title => %+v, want empty-title rename (cancel)", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "")), st, true); tr != nil {
		t.Errorf("still-cleared title => %+v, want nil (no change)", tr)
	}
	if tr := classifyEvent([]byte(paneEv("", "shell title")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("title set again after clearing => %+v, want rename", tr)
	}
	if tr := classifyEvent([]byte(`{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed"}}`), st, true); tr == nil || tr.kind != triggerTitle {
		t.Errorf("status change => %+v, want title-only", tr)
	}
	if tr := classifyEvent([]byte(`{"event":"tab_focused","data":{"type":"tab_focused"}}`), st, true); tr == nil || tr.kind != triggerFull {
		t.Errorf("tab focus => %+v, want full", tr)
	}
	if tr := classifyEvent([]byte(`not json`), st, true); tr != nil {
		t.Errorf("garbage => %+v, want nil", tr)
	}
}

func TestClassifyEventFocusGate(t *testing.T) {
	st := newClassifyState()
	paneEv := func(pane, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":%q,"tab_id":"w1:t1","agent":"","terminal_title_stripped":%q}}}`,
			pane, title)
	}

	// layout.updated declares w1:p1 the tab's focused pane and maps both panes.
	layoutEv := `{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w1:t1","focused_pane_id":"w1:p1","panes":[{"pane_id":"w1:p1"},{"pane_id":"w1:p2"}]}}}`
	if tr := classifyEvent([]byte(layoutEv), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("layout update => %+v, want full", tr)
	}

	if tr := classifyEvent([]byte(paneEv("w1:p1", "focused pane title")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("focused pane title => %+v, want rename", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p2", "background pane title")), st, true); tr != nil {
		t.Errorf("non-focused pane title => %+v, want nil (tab named after w1:p1)", tr)
	}

	// pane.focused moves the tab's focus to w1:p2. Its gated title was still
	// recorded, so replaying it dedups — the focus change's full pass already
	// applied it — while a genuinely new title renames.
	focusEv := `{"event":"pane_focused","data":{"type":"pane_focused","pane_id":"w1:p2","workspace_id":"w1"}}`
	if tr := classifyEvent([]byte(focusEv), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("pane focus => %+v, want full", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p2", "background pane title")), st, true); tr != nil {
		t.Errorf("recorded title replayed after focus switch => %+v, want nil", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p2", "fresh title")), st, true); tr == nil || tr.kind != triggerRename || !tr.pane.FocusKnown {
		t.Errorf("newly focused pane's new title => %+v, want rename with FocusKnown", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p1", "focused pane title 2")), st, true); tr != nil {
		t.Errorf("formerly focused pane => %+v, want nil", tr)
	}

	// A pane in a tab the classifier knows nothing about is passed through
	// unverified; the rename op resolves it via the tab's pane count.
	unknown := `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w9:p9","tab_id":"w9:t9","agent":"","terminal_title_stripped":"new"}}}`
	if tr := classifyEvent([]byte(unknown), st, true); tr == nil || tr.kind != triggerRename || tr.pane.FocusKnown {
		t.Errorf("unknown tab => %+v, want rename without FocusKnown", tr)
	}
}

func TestClassifyEventPrunesClosedPanes(t *testing.T) {
	st := newClassifyState()
	paneEv := func(pane, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":%q,"tab_id":"w1:t1","agent":"","terminal_title_stripped":%q}}}`,
			pane, title)
	}
	layoutEv := `{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w1:t1","focused_pane_id":"w1:p1","panes":[{"pane_id":"w1:p1"},{"pane_id":"w1:p2"}]}}}`
	closeEv := `{"event":"pane_closed","data":{"type":"pane_closed","pane_id":"w1:p1","workspace_id":"w1"}}`

	classifyEvent([]byte(layoutEv), st, true)
	if tr := classifyEvent([]byte(paneEv("w1:p1", "make")), st, true); tr == nil || tr.kind != triggerRename {
		t.Fatalf("set => %+v, want rename", tr)
	}
	if tr := classifyEvent([]byte(closeEv), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("close => %+v, want full", tr)
	}
	if len(st.lastTitles) != 0 || len(st.paneTab) != 1 || len(st.tabFocus) != 0 {
		t.Errorf("close did not prune: titles=%v paneTab=%v tabFocus=%v", st.lastTitles, st.paneTab, st.tabFocus)
	}
	// A recycled pane id must not dedup against the dead pane's title.
	if tr := classifyEvent([]byte(paneEv("w1:p1", "make")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("reused pane id deduped against stale title: %+v", tr)
	}
	// The surviving pane is no longer muted by the dead pane's focus entry.
	if tr := classifyEvent([]byte(paneEv("w1:p2", "other")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("surviving pane still muted after focus-holder closed: %+v", tr)
	}
}

func TestClassifyStateSeed(t *testing.T) {
	st := newClassifyState()
	st.seed(&Snapshot{
		Panes: []Pane{
			{PaneID: "w1:p1", TabID: "w1:t1"},
			{PaneID: "w1:p2", TabID: "w1:t1"},
		},
		TabFocus: map[string]string{"w1:t1": "w1:p1"},
	})
	paneEv := func(pane, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":%q,"tab_id":"w1:t1","agent":"","terminal_title_stripped":%q}}}`,
			pane, title)
	}

	if tr := classifyEvent([]byte(paneEv("w1:p2", "background")), st, true); tr != nil {
		t.Errorf("seeded focus did not gate the non-focused pane: %+v", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p1", "focused")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("seeded focused pane => %+v, want rename", tr)
	}

	focusEv := `{"event":"pane_focused","data":{"type":"pane_focused","pane_id":"w1:p2","workspace_id":"w1"}}`
	classifyEvent([]byte(focusEv), st, true)
	if tr := classifyEvent([]byte(paneEv("w1:p2", "fresh")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("focus switch via seeded paneTab => %+v, want rename", tr)
	}
}

// Ensure title changes are recorded even when a pane is unfocused,
// so that further changes after it becomes focused are not lost.
func TestClassifyEventUnfocusedTitlesRecorded(t *testing.T) {
	st := newClassifyState()
	paneEv := func(pane, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":%q,"tab_id":"w1:t1","agent":"","terminal_title_stripped":%q}}}`,
			pane, title)
	}
	layoutEv := `{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w1:t1","focused_pane_id":"w1:p1","panes":[{"pane_id":"w1:p1"},{"pane_id":"w1:p2"}]}}}`
	focusEv := `{"event":"pane_focused","data":{"type":"pane_focused","pane_id":"w1:p2","workspace_id":"w1"}}`

	classifyEvent([]byte(layoutEv), st, true)
	classifyEvent([]byte(paneEv("w1:p2", "A")), st, true)
	classifyEvent([]byte(paneEv("w1:p2", "B")), st, true)
	classifyEvent([]byte(focusEv), st, true)
	if tr := classifyEvent([]byte(paneEv("w1:p2", "A")), st, true); tr == nil || tr.kind != triggerRename || tr.pane.Title != "A" {
		t.Errorf("change back to a title recorded while unfocused => %+v, want rename to A", tr)
	}
}

// If a split's new pane arriving via pane.created is already focused, it must
// own the tab name immediately, because the pane.focused that follows cannot
// be attributed (the pane was never seen previously), and on old herdr without
// layout events nothing else would ever heal the focus gate.
func TestClassifyEventPaneCreatedTakesFocus(t *testing.T) {
	st := newClassifyState()
	paneEv := func(pane, title string) string {
		return fmt.Sprintf(
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":%q,"tab_id":"w1:t1","agent":"","terminal_title_stripped":%q}}}`,
			pane, title)
	}
	layoutEv := `{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w1:t1","focused_pane_id":"w1:p1","panes":[{"pane_id":"w1:p1"}]}}}`
	created := `{"event":"pane_created","data":{"type":"pane_created","pane":{"pane_id":"w1:p2","tab_id":"w1:t1","focused":true,"agent":"","terminal_title_stripped":""}}}`

	classifyEvent([]byte(layoutEv), st, true)
	if tr := classifyEvent([]byte(created), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("pane created => %+v, want full", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p2", "new pane")), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("freshly created focused pane => %+v, want rename", tr)
	}
	if tr := classifyEvent([]byte(paneEv("w1:p1", "old pane")), st, true); tr != nil {
		t.Errorf("pre-split pane still names the tab: %+v", tr)
	}
}

// A cross-tab move re-identifies the pane. The previous ID's state must not
// linger, and the source tab must not retain the lost pane as its focus.
func TestClassifyEventPaneMovedReidentifies(t *testing.T) {
	st := newClassifyState()
	layoutEv := `{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w1:t1","focused_pane_id":"w1:p1","panes":[{"pane_id":"w1:p1"}]}}}`
	titleEv := `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","tab_id":"w1:t1","agent":"","terminal_title_stripped":"make"}}}`
	movedEv := `{"event":"pane_moved","data":{"type":"pane_moved","pane":{"pane_id":"w2:p5","tab_id":"w2:t1","focused":true,"agent":"","terminal_title_stripped":"make"},"previous_pane_id":"w1:p1","previous_tab_id":"w1:t1"}}`

	classifyEvent([]byte(layoutEv), st, true)
	classifyEvent([]byte(titleEv), st, true)
	if tr := classifyEvent([]byte(movedEv), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("pane moved => %+v, want full", tr)
	}
	if _, ok := st.paneTab["w1:p1"]; ok {
		t.Error("previous pane id not pruned from paneTab")
	}
	if _, ok := st.lastTitles["w1:p1"]; ok {
		t.Error("previous pane id not pruned from lastTitles")
	}
	if _, ok := st.tabFocus["w1:t1"]; ok {
		t.Error("source tab still focused on the departed pane")
	}
	if st.paneTab["w2:p5"] != "w2:t1" || st.tabFocus["w2:t1"] != "w2:p5" {
		t.Errorf("moved pane not tracked at its new identity: paneTab=%v tabFocus=%v",
			st.paneTab, st.tabFocus)
	}
	// The re-identified pane does not inherit the old ID's dedup title.
	sameTitle := `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w2:p5","tab_id":"w2:t1","agent":"","terminal_title_stripped":"make"}}}`
	if tr := classifyEvent([]byte(sameTitle), st, true); tr == nil || tr.kind != triggerRename {
		t.Errorf("moved pane deduped against its previous identity: %+v", tr)
	}
}

// Closing a tab or workspace emits no per-pane events, so the tab- and
// workspace-level events must prune everything they owned.
func TestClassifyEventPrunesClosedTabsAndWorkspaces(t *testing.T) {
	st := newClassifyState()
	seed := func() {
		for _, ev := range []string{
			`{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w1:t1","focused_pane_id":"w1:p1","panes":[{"pane_id":"w1:p1"},{"pane_id":"w1:p2"}]}}}`,
			`{"event":"layout_updated","data":{"type":"layout_updated","layout":{"tab_id":"w2:t1","focused_pane_id":"w2:p1","panes":[{"pane_id":"w2:p1"}]}}}`,
			`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","tab_id":"w1:t1","agent":"","terminal_title_stripped":"make"}}}`,
		} {
			classifyEvent([]byte(ev), st, true)
		}
	}

	seed()
	closeTab := `{"event":"tab_closed","data":{"type":"tab_closed","tab_id":"w1:t1","workspace_id":"w1"}}`
	if tr := classifyEvent([]byte(closeTab), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("tab close => %+v, want full", tr)
	}
	if len(st.tabFocus) != 1 || len(st.paneTab) != 1 || len(st.lastTitles) != 0 {
		t.Errorf("tab close did not prune its tab: titles=%v paneTab=%v tabFocus=%v",
			st.lastTitles, st.paneTab, st.tabFocus)
	}

	seed()
	closeWs := `{"event":"workspace_closed","data":{"type":"workspace_closed","workspace_id":"w1"}}`
	if tr := classifyEvent([]byte(closeWs), st, true); tr == nil || tr.kind != triggerFull {
		t.Fatalf("workspace close => %+v, want full", tr)
	}
	if len(st.tabFocus) != 1 || len(st.paneTab) != 1 || len(st.lastTitles) != 0 {
		t.Errorf("workspace close did not prune its tabs: titles=%v paneTab=%v tabFocus=%v",
			st.lastTitles, st.paneTab, st.tabFocus)
	}
	if _, ok := st.tabFocus["w2:t1"]; !ok {
		t.Error("workspace close pruned another workspace's tab")
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
	go func() { runScheduler(triggers, rec.ops(), testTimings(), stop); close(done) }()

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

	// A set-then-clear inside one debounce window: the clear overwrites the
	// queued set, so only the empty-title (fallback) rename reaches the op.
	fullsBefore, _, renamesBefore := rec.snapshot()
	triggers <- trigger{kind: triggerRename, pane: paneEvent{PaneID: "p", TabID: "t1", Title: "transient"}}
	triggers <- trigger{kind: triggerRename, pane: paneEvent{PaneID: "p", TabID: "t1", Title: ""}}
	time.Sleep(80 * time.Millisecond)
	fulls, _, renames = rec.snapshot()
	if got := renames[len(renamesBefore):]; len(got) != 1 || got[0] != "t1=" {
		t.Fatalf("set-then-clear => %v, want the single empty-title rename", got)
	}
	if fulls != fullsBefore {
		t.Fatalf("set-then-clear escalated to a full pass: %d -> %d", fullsBefore, fulls)
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
	// rejectLayoutSub mimics an older herdr: a subscribe with layout.updated
	// is refused, only the retry without it can succeed.
	rejectLayoutSub atomic.Bool
}

func newFakeEventServer(t *testing.T, pingOK bool) *fakeEventServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "hwtw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return newFakeEventServerAt(t, filepath.Join(dir, "herdr.sock"), pingOK)
}

// newFakeEventServerAt binds at an explicit socket path — herdr reuses the
// same sessions/<name>/herdr.sock across session restarts, so lifecycle
// tests need a second server at the first one's address.
func newFakeEventServerAt(t *testing.T, sockPath string, pingOK bool) *fakeEventServer {
	t.Helper()
	s := &fakeEventServer{
		t:        t,
		sockPath: sockPath,
		subGot:   make(chan string, 1),
		events:   make(chan string, 16),
		closeSub: make(chan struct{}),
		pingOK:   pingOK,
	}
	var err error
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
			if s.rejectLayoutSub.Load() && strings.Contains(line, "layout.updated") {
				conn.Write([]byte(`{"id":"watch","error":{"code":"invalid_request","message":"unknown event type"}}` + "\n"))
				conn.Close()
				continue
			}
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
		for _, want := range []string{
			"pane.updated", "tab.focused", "pane.agent_detected",
			"layout.updated", "tab.closed", "workspace.closed",
		} {
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

func TestWatchDaemonFallsBackWithoutLayoutSubscription(t *testing.T) {
	srv := newFakeEventServer(t, true)
	srv.rejectLayoutSub.Store(true)
	rec := &opsRecorder{}
	stateDir := t.TempDir()

	done := make(chan error, 1)
	go func() {
		done <- watchDaemon(srv.sockPath, stateDir, "wtest", nil, rec.ops(), testTimings())
	}()

	select {
	case sub := <-srv.subGot:
		for _, optional := range optionalSubscriptions {
			if strings.Contains(sub, optional) {
				t.Fatalf("retry still asked for %s: %s", optional, sub)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon never landed the fallback subscription")
	}

	// The degraded stream still works end to end.
	srv.events <- `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","tab_id":"w1:t1","agent":"claude","terminal_title_stripped":"Renamed"}}}`
	time.Sleep(100 * time.Millisecond)
	_, _, renames := rec.snapshot()
	if len(renames) != 1 || renames[0] != "w1:t1=Renamed" {
		t.Fatalf("renames = %v, want [w1:t1=Renamed]", renames)
	}

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

func TestWatchDaemonStopsWithSessionAndRestarts(t *testing.T) {
	// One session lifecycle end to end: stopping the session's server must
	// take the daemon down with it (releasing the singleton flock), and a
	// fresh daemon for the restarted session — same socket path, same state
	// dir — must acquire the lock and subscribe.
	srv1 := newFakeEventServer(t, true)
	stateDir := t.TempDir()

	done1 := make(chan error, 1)
	go func() {
		done1 <- watchDaemon(srv1.sockPath, stateDir, "wtest", nil, (&opsRecorder{}).ops(), testTimings())
	}()
	<-srv1.subGot

	// Session stops: the server and its connections go away.
	srv1.ln.Close()
	close(srv1.closeSub)
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon survived its session's stop")
	}
	if daemonAlive(stateDir, "wtest") {
		t.Fatal("exited daemon still holds the liveness lock")
	}

	// Session starts again at the same socket path.
	srv2 := newFakeEventServerAt(t, srv1.sockPath, true)
	done2 := make(chan error, 1)
	go func() {
		done2 <- watchDaemon(srv2.sockPath, stateDir, "wtest", nil, (&opsRecorder{}).ops(), testTimings())
	}()
	select {
	case <-srv2.subGot:
	case <-time.After(2 * time.Second):
		t.Fatal("restarted session did not get a fresh daemon subscription")
	}
	close(srv2.closeSub)
	if err := <-done2; err != nil {
		t.Fatalf("second daemon exit error: %v", err)
	}
}

var _ = json.Marshal // placate imports during TDD scaffolding

func TestWatchParentSpawnsForDefaultSession(t *testing.T) {
	// The default (unnamed) session sets HERDR_SOCKET_PATH but not
	// HERDR_SESSION, and the parent must still proceed. An invalid config
	// file makes it stop with a parse error right after the env gate —
	// proof the gate passed, without actually daemonizing.
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"), []byte("not hcl {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_SOCKET_PATH", "/nonexistent.sock")
	t.Setenv("HERDR_SESSION", "")

	if err := runWatchParent(); err == nil {
		t.Fatal("parent bailed at the env gate for the default session")
	}

	// Without a server socket there is nothing to watch: the gate must hold.
	t.Setenv("HERDR_SOCKET_PATH", "")
	if err := runWatchParent(); err != nil {
		t.Fatalf("parent did not gate on missing socket: %v", err)
	}
}

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

func TestWatchDaemonRestartsOnBinaryChange(t *testing.T) {
	srv := newFakeEventServer(t, true)
	stateDir := t.TempDir()
	binPath := filepath.Join(stateDir, "fake-binary")
	if err := os.WriteFile(binPath, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	restarted := make(chan string, 1)
	orig := restartSelf
	restartSelf = func(path string) { restarted <- path }
	defer func() { restartSelf = orig }()

	timings := testTimings()
	timings.BinaryPoll = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		done <- watchDaemonAt(srv.sockPath, stateDir, "wtest", binPath, false, nil, (&opsRecorder{}).ops(), timings)
	}()
	<-srv.subGot

	// Untouched binary: no restart.
	select {
	case p := <-restarted:
		t.Fatalf("restart without binary change: %s", p)
	case <-time.After(120 * time.Millisecond):
	}

	// Replace the binary (new mtime/size): the daemon must restart itself.
	if err := os.WriteFile(binPath, []byte("v2-bigger"), 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-restarted:
		if p != binPath {
			t.Errorf("restarted with %q, want %q", p, binPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("binary change never triggered a restart")
	}

	close(srv.closeSub)
	<-done
}

func TestWatchDaemonRestartsOnBinaryRemoval(t *testing.T) {
	srv := newFakeEventServer(t, true)
	stateDir := t.TempDir()
	binPath := filepath.Join(stateDir, "fake-binary")
	if err := os.WriteFile(binPath, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarted := make(chan string, 1)
	orig := restartSelf
	restartSelf = func(path string) { restarted <- path }
	defer func() { restartSelf = orig }()

	timings := testTimings()
	timings.BinaryPoll = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		done <- watchDaemonAt(srv.sockPath, stateDir, "wtest", binPath, false, nil, (&opsRecorder{}).ops(), timings)
	}()
	<-srv.subGot

	// Removal (uninstall): after consecutive misses the daemon hands off —
	// the real restartSelf exec-fails on a missing file and exits.
	if err := os.Remove(binPath); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("binary removal never triggered a handoff")
	}
	close(srv.closeSub)
	<-done
}

func TestAnnounceRestartRetriesUntilShown(t *testing.T) {
	api := newFakeAPI(t)
	api.mu.Lock()
	api.notifBusy = 2 // typing: two refusals before the toast lands
	api.mu.Unlock()

	if !announceRestart(api.sockPath, "Updated to 0.9.0 — daemon restarted", time.Millisecond, 10) {
		t.Fatal("announceRestart = false, want shown")
	}
	got := api.notified()
	want := "herdr-titles|Updated to 0.9.0 — daemon restarted"
	if len(got) != 3 || got[2] != want {
		t.Errorf("notifications = %v, want three ending in %q", got, want)
	}
}

func TestAnnounceRestartStopsWhenDisabled(t *testing.T) {
	api := newFakeAPI(t)
	api.mu.Lock()
	api.notifDisabled = true // toasts off in herdr config: never retry
	api.mu.Unlock()

	if announceRestart(api.sockPath, "body", time.Millisecond, 10) {
		t.Fatal("announceRestart = true against disabled toasts")
	}
	if got := api.notified(); len(got) != 1 {
		t.Errorf("notifications = %v, want exactly one attempt", got)
	}
}

func TestAnnounceRestartGivesUpAfterMaxAttempts(t *testing.T) {
	api := newFakeAPI(t)
	api.mu.Lock()
	api.notifBusy = 100
	api.mu.Unlock()

	if announceRestart(api.sockPath, "body", time.Millisecond, 3) {
		t.Fatal("announceRestart = true, want give-up")
	}
	if got := api.notified(); len(got) != 3 {
		t.Errorf("notifications = %v, want exactly three attempts", got)
	}
}

func TestPluginVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id = \"davidolrik.titles\"\nversion = \"0.9.0\"\n"
	if err := os.WriteFile(filepath.Join(root, "herdr-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := pluginVersion(filepath.Join(root, "bin", "herdr-titles")); got != "0.9.0" {
		t.Errorf("pluginVersion = %q, want 0.9.0", got)
	}
	if got := pluginVersion(filepath.Join(t.TempDir(), "bin", "herdr-titles")); got != "" {
		t.Errorf("pluginVersion without manifest = %q, want empty", got)
	}
}

func TestWithAnnounceEnv(t *testing.T) {
	env := withAnnounceEnv([]string{"HOME=/x"})
	if len(env) != 2 || env[1] != announceEnvVar+"=1" {
		t.Errorf("env = %v, want marker appended", env)
	}
	// Idempotent: a marker already present is not duplicated.
	if again := withAnnounceEnv(env); len(again) != 2 {
		t.Errorf("marker duplicated: %v", again)
	}
}
