package main

// Direct client for herdr's session API socket. Every pass used to fork+exec
// the herdr CLI for its I/O (snapshot, tab get/rename, title set,
// process-info); that overhead dwarfed the actual work. The protocol is
// newline-delimited JSON — one request line, one response line — and dialing
// a unix socket costs microseconds, so each request simply dials fresh: no
// pooling, no reconnect state, nothing to go stale.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// sessionSocketPath is where herdr's plugin engine (and every pane) says the
// session API socket lives.
func sessionSocketPath() string {
	return os.Getenv("HERDR_SOCKET_PATH")
}

// apiRequest sends one request and returns the response's result payload.
// A server-side error response becomes a Go error carrying its message.
func apiRequest(sockPath, method string, params any) (json.RawMessage, error) {
	if sockPath == "" {
		return nil, fmt.Errorf("%s: HERDR_SOCKET_PATH not set", method)
	}
	conn, err := net.DialTimeout("unix", sockPath, socketTimeout)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(socketTimeout))

	req := struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{ID: "herdr-titles", Method: method, Params: params}
	if req.Params == nil {
		req.Params = struct{}{}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}
