package main

import (
	"os"
	"testing"
)

func TestReadAntigravityTitle(t *testing.T) {
	cwd, _ := os.Getwd()
	title := readAntigravityTitle(cwd)
	t.Logf("readAntigravityTitle for %q => %q", cwd, title)
	if title != "Test" {
		t.Errorf("got %q, want %q", title, "Test")
	}
}
