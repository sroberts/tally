package herdr

import (
	"errors"
	"net"
	"testing"
)

// A socket that takes the connection but not the request is offline, as a
// refused dial is.
func TestFailedRequestWriteIsOffline(t *testing.T) {
	old := dialSocket
	t.Cleanup(func() { dialSocket = old })
	dialSocket = func(string) (net.Conn, error) {
		client, server := net.Pipe()
		server.Close() // writes to client now fail at once
		return client, nil
	}
	for name, call := range map[string]func() error{
		"read":  func() error { _, err := New("/x").Snapshot(); return err },
		"write": func() error { return NewWriter("/x").Focus("w1:p1") },
	} {
		var off *OfflineError
		if err := call(); !errors.As(err, &off) || off.Path != "/x" {
			t.Errorf("%s: err = %v, want an *OfflineError", name, err)
		}
	}
}
