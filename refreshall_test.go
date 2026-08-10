package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSessionSocket listens on a session-style socket and records the first
// line each connection sends, answering with a minimal API response.
func fakeSessionSocket(t *testing.T, path string, got chan<- string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				got <- line
				c.Write([]byte(`{"id":"refresh-all","result":{"type":"plugin_action_invoked"}}` + "\n"))
			}(conn)
		}
	}()
}

func TestRefreshAll(t *testing.T) {
	dir, err := os.MkdirTemp("", "hwt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	got := make(chan string, 4)
	fakeSessionSocket(t, filepath.Join(dir, "sessions", "s1", "herdr.sock"), got)
	fakeSessionSocket(t, filepath.Join(dir, "sessions", "s2", "herdr.sock"), got)
	// A stopped session's leftover socket: exists but nothing listens.
	dead := filepath.Join(dir, "sessions", "s3", "herdr.sock")
	if err := os.MkdirAll(filepath.Dir(dead), 0o755); err != nil {
		t.Fatal(err)
	}
	deadLn, err := net.Listen("unix", dead)
	if err != nil {
		t.Fatal(err)
	}
	deadLn.Close() // closed listener leaves the socket file refusing connects

	if err := RefreshAll(dir); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case line := <-got:
			if !strings.Contains(line, `"plugin.action.invoke"`) ||
				!strings.Contains(line, `"davidolrik.titles"`) ||
				!strings.Contains(line, `"refresh"`) {
				t.Errorf("payload %d = %q, want a refresh action invoke", i, line)
			}
			if !strings.HasSuffix(line, "\n") {
				t.Errorf("payload %d not newline-terminated", i)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("session %d never received a request", i)
		}
	}
	select {
	case extra := <-got:
		t.Errorf("unexpected extra request: %q", extra)
	default:
	}
}

func TestRefreshAllNoSessions(t *testing.T) {
	dir := t.TempDir()
	if err := RefreshAll(dir); err != nil {
		t.Fatalf("RefreshAll with no sessions: %v", err)
	}
}
