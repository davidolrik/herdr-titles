package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// ApplyTitle pushes title to the terminal via herdr, skipping the call when it
// matches the last title recorded at statePath. Herdr keeps an explicitly-set
// client title until it is overwritten, so an unchanged title needs no push.
func ApplyTitle(herdrBin, statePath, title string) (bool, error) {
	if last, err := os.ReadFile(statePath); err == nil && string(last) == title {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, herdrBin, "terminal", "title", "set", title)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s terminal title set: %w: %s", herdrBin, err, stderr.String())
	}

	if err := os.WriteFile(statePath, []byte(title), 0o600); err != nil {
		return true, fmt.Errorf("record last title: %w", err)
	}
	return true, nil
}
