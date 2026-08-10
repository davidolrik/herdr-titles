package main

// refresh-all: poke every running herdr session to run this plugin's refresh
// action. Meant for hooks of external tools (e.g. Overseer's context change):
// such hooks run in a daemon context where the herdr CLI can only reach one
// session, and looping over session sockets in shell needs socat + jq. This
// speaks herdr's newline-delimited JSON socket protocol directly, so the hook
// reduces to invoking this binary — no extra dependencies.

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const socketTimeout = 5 * time.Second

// refreshSession invokes the plugin's refresh action over one session socket.
func refreshSession(sock string) error {
	conn, err := net.DialTimeout("unix", sock, socketTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(socketTimeout))

	payload := fmt.Sprintf(
		`{"id":"refresh-all","method":"plugin.action.invoke","params":{"plugin_id":%q,"action_id":"refresh"}}`,
		pluginID)
	if _, err := fmt.Fprintf(conn, "%s\n", payload); err != nil {
		return err
	}
	// Wait for the response so the server has accepted the invoke before we
	// move on; the content doesn't matter.
	_, err = bufio.NewReader(conn).ReadString('\n')
	return err
}

// RefreshAll walks every session socket under herdrConfigDir — named sessions
// under sessions/<name>/, plus the default session's socket in the dir root —
// and invokes the refresh action on each. Sockets that refuse the connection
// (stopped sessions leave their socket files behind) are skipped silently;
// the whole sweep never fails.
func RefreshAll(herdrConfigDir string) error {
	socks, _ := filepath.Glob(filepath.Join(herdrConfigDir, "sessions", "*", "herdr.sock"))
	socks = append(socks, filepath.Join(herdrConfigDir, "herdr.sock"))

	for _, sock := range socks {
		if info, err := os.Stat(sock); err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		session := filepath.Base(filepath.Dir(sock))
		if err := refreshSession(sock); err != nil {
			fmt.Printf("skipped %s: %v\n", session, err)
			continue
		}
		fmt.Printf("refreshed %s\n", session)
	}
	return nil
}

// runRefreshAll resolves herdr's config dir the same way herdr does.
func runRefreshAll() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return RefreshAll(filepath.Join(home, ".config", "herdr"))
}
