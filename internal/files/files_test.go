package files

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestWritePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "new" {
		t.Fatalf("read %q, %v", b, err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
}

// Read-modify-write under Locked loses nothing, even from many goroutines
// at once: each opens the lock file itself, as separate processes would.
func TestLockedSerializes(t *testing.T) {
	dir := t.TempDir()
	path, lock := filepath.Join(dir, "n"), filepath.Join(dir, "n.lock")
	if err := WritePrivate(path, []byte("0")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			err := Locked(lock, func() error {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				n, _ := strconv.Atoi(string(b))
				return WritePrivate(path, []byte(strconv.Itoa(n+1)))
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if b, _ := os.ReadFile(path); string(b) != "20" {
		t.Errorf("count %s, want 20", b)
	}
	fi, err := os.Stat(lock)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("lock file: %v %v", fi, err)
	}
}
