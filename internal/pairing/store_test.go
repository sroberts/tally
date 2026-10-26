package pairing

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Pairing and revoking at the same time, as tally pair in two terminals
// would, keeps every write: remote.json is updated under a lock.
func TestConcurrentIssueAndRevokeLoseNothing(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for i := range 10 {
		if _, err := s.Issue(fmt.Sprintf("old-%d", i), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			if _, err := s.Issue(fmt.Sprintf("new-%d", i), time.Now()); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := s.Revoke(fmt.Sprintf("old-%d", i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Phones) != 10 {
		t.Fatalf("%d phones paired, want the 10 new ones: %+v", len(st.Phones), st.Phones)
	}
	for _, p := range st.Phones {
		if p.Label[:4] != "new-" {
			t.Errorf("%s survived its revoke", p.Label)
		}
	}
}

// Two first uses at once agree on one host id.
func TestConcurrentHostIDAgree(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			id, err := s.HostID()
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("host ids %q", ids)
		}
	}
	if st, _ := s.Load(); st.HostID != ids[0] {
		t.Errorf("remote.json has %q, callers got %q", st.HostID, ids[0])
	}
}
