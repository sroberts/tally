// Package files writes tally's private state: files only this user may
// read, replaced atomically, and locks that keep tally serve and the CLI
// from losing each other's writes.
//
// Locks are flock(2), so tally builds for macOS and Linux only.
package files

import (
	"os"
	"syscall"
)

// WritePrivate writes b to path with mode 0600, atomically: readers see
// the old file or the new one, never half of either. Callers that may race
// hold a Lock, since every writer of path shares one temporary file.
func WritePrivate(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // WriteFile keeps an old file's mode
		return err
	}
	return os.Rename(tmp, path)
}

// Locked runs f holding an exclusive lock on the file at lock, making it
// (mode 0600) if needed. The lock is per open file, so f must not take the
// same lock again.
func Locked(lock string, f func() error) error {
	l, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer l.Close()
	if err := syscall.Flock(int(l.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(l.Fd()), syscall.LOCK_UN)
	return f()
}
