package main

// Run coalescing, ported from qu8n/herdr-automatic-rename's lock protocol
// (MIT, (c) Quan Nguyen). Herdr spawns one plugin process per event and caps
// concurrent plugin commands (32 in herdr 0.8.0); a rename storm — e.g. this
// plugin's own renames re-firing tab.renamed — must not pile processes up in
// a queue. So the lock is NONBLOCKING: a contender raises the rerun flag and
// exits immediately, freeing its slot, and the flag makes the current lock
// holder run one more (idempotent, superset) pass before releasing.

import (
	"os"
	"path/filepath"
	"syscall"
)

// withLock runs fn under the PER-SESSION lock: every pass is scoped to one
// herdr session by its environment, so a contender from another session must
// never hand its work to this session's holder — its rerun pass would
// reconcile the wrong session's tabs and titles. Within a session, a
// contender touches the rerun flag and returns nil without running fn; the
// holder re-runs fn while the flag keeps appearing, capped at 8 passes.
func withLock(stateDir, session string, fn func() error) error {
	lockFile, err := os.OpenFile(filepath.Join(stateDir, "lock."+session), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	rerun := filepath.Join(stateDir, "rerun."+session)
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Someone else is reconciling; hand them the work.
		return os.WriteFile(rerun, nil, 0o600)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	for pass := 0; pass < 8; pass++ {
		os.Remove(rerun)
		if err := fn(); err != nil {
			return err
		}
		if _, err := os.Stat(rerun); os.IsNotExist(err) {
			return nil
		}
	}
	return nil
}
