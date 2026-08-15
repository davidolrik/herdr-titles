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
	"testing"
)

// fakeAPI is a herdr API socket stand-in: one request line per connection,
// routed by method, mutations recorded. It replaces the fake-CLI scripts the
// tests used before the plugin spoke the socket protocol directly.
type fakeAPI struct {
	sockPath string
	ln       net.Listener

	mu            sync.Mutex
	snapshot      json.RawMessage            // result for session.snapshot
	tabLabels     map[string]string          // tab.get labels
	tabPanes      map[string]int             // tab.get pane_count (default 1)
	tabUnfocused  map[string]bool            // tab.get focused=false when set
	paneTitles    map[string][2]string       // pane.get {agent, terminal_title_stripped}
	paneUnfocused map[string]bool            // pane.get focused=false when set
	processInfos  map[string]json.RawMessage // pane_id -> process_info payload
	titleSets     []string
	renames       []string
	snapshots     int
	infoReqs      int

	notifications []string // recorded "title|body" per notification.show
	notifBusy     int      // answer "busy" this many times before showing
	notifDisabled bool     // always answer "disabled"
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	dir, err := os.MkdirTemp("", "hwta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeAPI{
		sockPath:      filepath.Join(dir, "herdr.sock"),
		tabLabels:     map[string]string{},
		tabPanes:      map[string]int{},
		tabUnfocused:  map[string]bool{},
		paneTitles:    map[string][2]string{},
		paneUnfocused: map[string]bool{},
		processInfos:  map[string]json.RawMessage{},
	}
	f.ln, err = net.Listen("unix", f.sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.ln.Close() })
	go f.serve()
	return f
}

func (f *fakeAPI) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				return
			}
			var req struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal([]byte(line), &req) != nil {
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			reply := func(result string) {
				fmt.Fprintf(c, `{"id":%q,"result":%s}`+"\n", req.ID, result)
			}
			fail := func(code, msg string) {
				fmt.Fprintf(c, `{"id":%q,"error":{"code":%q,"message":%q}}`+"\n", req.ID, code, msg)
			}
			switch req.Method {
			case "session.snapshot":
				f.snapshots++
				if f.snapshot == nil {
					fail("unavailable", "no snapshot configured")
					return
				}
				reply(string(f.snapshot))
			case "client.window_title.set":
				var p struct {
					Title string `json:"title"`
				}
				_ = json.Unmarshal(req.Params, &p)
				f.titleSets = append(f.titleSets, p.Title)
				reply(`{"type":"client_window_title","changed":true,"reason":"set"}`)
			case "tab.get":
				var p struct {
					TabID string `json:"tab_id"`
				}
				_ = json.Unmarshal(req.Params, &p)
				label, ok := f.tabLabels[p.TabID]
				if !ok {
					fail("not_found", "no such tab")
					return
				}
				paneCount := f.tabPanes[p.TabID]
				if paneCount == 0 {
					paneCount = 1
				}
				// json.Marshal, not %q: Go quoting writes PUA glyphs as \U
				// escapes, which are not valid JSON.
				tab, _ := json.Marshal(map[string]any{
					"tab_id": p.TabID, "label": label,
					"pane_count": paneCount, "focused": !f.tabUnfocused[p.TabID],
				})
				reply(`{"type":"tab_info","tab":` + string(tab) + `}`)
			case "tab.rename":
				var p struct {
					TabID string `json:"tab_id"`
					Label string `json:"label"`
				}
				_ = json.Unmarshal(req.Params, &p)
				f.renames = append(f.renames, p.TabID+"="+p.Label)
				f.tabLabels[p.TabID] = p.Label
				reply(`{"type":"tab_info"}`)
			case "notification.show":
				var p struct {
					Title string `json:"title"`
					Body  string `json:"body"`
				}
				_ = json.Unmarshal(req.Params, &p)
				f.notifications = append(f.notifications, p.Title+"|"+p.Body)
				switch {
				case f.notifDisabled:
					reply(`{"type":"notification_show","shown":false,"reason":"disabled"}`)
				case f.notifBusy > 0:
					f.notifBusy--
					reply(`{"type":"notification_show","shown":false,"reason":"busy"}`)
				default:
					reply(`{"type":"notification_show","shown":true,"reason":"shown"}`)
				}
			case "pane.get":
				var p struct {
					PaneID string `json:"pane_id"`
				}
				_ = json.Unmarshal(req.Params, &p)
				info, ok := f.paneTitles[p.PaneID]
				if !ok {
					fail("not_found", "no such pane")
					return
				}
				pane, _ := json.Marshal(map[string]any{
					"pane_id": p.PaneID, "agent": info[0], "terminal_title_stripped": info[1],
					"focused": !f.paneUnfocused[p.PaneID],
				})
				reply(`{"type":"pane_info","pane":` + string(pane) + `}`)
			case "pane.process_info":
				var p struct {
					PaneID string `json:"pane_id"`
				}
				_ = json.Unmarshal(req.Params, &p)
				f.infoReqs++
				info, ok := f.processInfos[p.PaneID]
				if !ok {
					fail("not_found", "no process info")
					return
				}
				fmt.Fprintf(c, `{"id":%q,"result":{"process_info":%s}}`+"\n", req.ID, info)
			default:
				fail("unknown_method", req.Method)
			}
		}(conn)
	}
}

func (f *fakeAPI) recorded() (titleSets, renames []string, snapshots int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.titleSets...), append([]string{}, f.renames...), f.snapshots
}

func (f *fakeAPI) notified() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.notifications...)
}

func (f *fakeAPI) setTab(tabID, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tabLabels[tabID] = label
}

// setPaneTitle registers a pane.get answer: its agent kind ("" for a plain
// pane) and stripped terminal title.
func (f *fakeAPI) setPaneTitle(paneID, agent, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneTitles[paneID] = [2]string{agent, title}
}

// setPaneUnfocused makes pane.get report the pane as not globally focused.
func (f *fakeAPI) setPaneUnfocused(paneID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneUnfocused[paneID] = true
}

// setTabShape overrides tab.get's pane_count and focused for one tab
// (defaults: single pane, focused).
func (f *fakeAPI) setTabShape(tabID string, paneCount int, focused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tabPanes[tabID] = paneCount
	f.tabUnfocused[tabID] = !focused
}

// setProcessInfoArgv registers a process whose argv differs from its argv0 —
// the interpreter shape (python running a console script).
func (f *fakeAPI) setProcessInfoArgv(paneID, argv0 string, argv []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, _ := json.Marshal(map[string]any{
		"foreground_process_group_id": 100,
		"foreground_processes": []map[string]any{
			{"pid": 100, "argv0": argv0, "argv": argv, "cmdline": strings.Join(argv, " "), "name": "on-disk-name"},
		},
	})
	f.processInfos[paneID] = info
}

func (f *fakeAPI) setProcessInfo(paneID, argv0, cmdline string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processInfos[paneID] = json.RawMessage(fmt.Sprintf(
		`{"foreground_process_group_id":100,"foreground_processes":[{"pid":100,"argv0":%q,"cmdline":%q,"name":"on-disk-name"}]}`,
		argv0, cmdline))
}

func TestAPIRequest(t *testing.T) {
	f := newFakeAPI(t)
	f.setTab("w1:t1", "hello")

	result, err := apiRequest(f.sockPath, "tab.get", map[string]string{"tab_id": "w1:t1"})
	if err != nil {
		t.Fatalf("apiRequest: %v", err)
	}
	if !strings.Contains(string(result), `"label":"hello"`) {
		t.Errorf("result = %s", result)
	}

	// Error responses become Go errors carrying the server's message.
	_, err = apiRequest(f.sockPath, "tab.get", map[string]string{"tab_id": "nope"})
	if err == nil || !strings.Contains(err.Error(), "no such tab") {
		t.Errorf("error response not surfaced: %v", err)
	}

	// Unknown socket path and empty socket path fail cleanly.
	if _, err := apiRequest(filepath.Join(t.TempDir(), "dead.sock"), "ping", nil); err == nil {
		t.Error("dead socket did not error")
	}
	if _, err := apiRequest("", "ping", nil); err == nil {
		t.Error("empty socket path did not error")
	}
}
