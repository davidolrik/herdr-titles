package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWithLockRunsAndReruns(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	err := withLock(dir, "s1", func() error {
		calls++
		if calls == 1 {
			// Work arrived while we held the lock: raise the rerun flag the
			// way a contender would.
			if err := os.WriteFile(filepath.Join(dir, "rerun.s1"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("fn ran %d times, want 2 (initial + one rerun)", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "rerun.s1")); !os.IsNotExist(err) {
		t.Error("rerun flag not consumed")
	}
}

func TestWithLockContenderRaisesFlagAndExits(t *testing.T) {
	dir := t.TempDir()

	// Hold the lock the way another plugin process would.
	holder, err := os.OpenFile(filepath.Join(dir, "lock.s1"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	calls := 0
	if err := withLock(dir, "s1", func() error { calls++; return nil }); err != nil {
		t.Fatalf("contender errored: %v", err)
	}
	if calls != 0 {
		t.Error("contender ran the pass despite the held lock")
	}
	if _, err := os.Stat(filepath.Join(dir, "rerun.s1")); err != nil {
		t.Errorf("contender did not raise the rerun flag: %v", err)
	}
}

func TestWithLockRerunGuardCapsLoop(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	err := withLock(dir, "s1", func() error {
		calls++
		// Pathological: work "arrives" after every pass, forever.
		return os.WriteFile(filepath.Join(dir, "rerun.s1"), nil, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 8 {
		t.Errorf("fn ran %d times, want the 8-pass guard", calls)
	}
}

// Sessions are independent: a pass for one herdr session must never be
// swallowed by another session's lock holder — passes are session-scoped, so
// cross-session coalescing silently drops work.
func TestWithLockSessionsDoNotContend(t *testing.T) {
	dir := t.TempDir()
	holder, err := os.OpenFile(filepath.Join(dir, "lock.other"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	calls := 0
	if err := withLock(dir, "mine", func() error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("session blocked by another session's lock: calls = %d", calls)
	}
}
