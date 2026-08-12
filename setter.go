package main

import (
	"fmt"
	"os"
)

// ApplyTitle pushes title to the terminal over the session socket, skipping
// the call when it matches the last title recorded at statePath. Herdr keeps
// an explicitly-set client title until it is overwritten, so an unchanged
// title needs no push.
func ApplyTitle(sockPath, statePath, title string) (bool, error) {
	if last, err := os.ReadFile(statePath); err == nil && string(last) == title {
		return false, nil
	}

	if _, err := apiRequest(sockPath, "client.window_title.set", map[string]string{"title": title}); err != nil {
		return false, err
	}

	if err := os.WriteFile(statePath, []byte(title), 0o600); err != nil {
		return true, fmt.Errorf("record last title: %w", err)
	}
	return true, nil
}
