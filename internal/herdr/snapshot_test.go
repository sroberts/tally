package herdr

import (
	"encoding/json"
	"testing"
)

func TestDecoderKeepsSpaceNames(t *testing.T) {
	var resp struct {
		Result struct {
			Snapshot Snapshot `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(liveResponse(t)), &resp); err != nil {
		t.Fatal(err)
	}
	for _, w := range resp.Result.Snapshot.Workspaces {
		if want := "space-label-" + string(rune('0'+w.Number)); w.Label != want {
			t.Errorf("workspace %s: label %q, want %q", w.ID, w.Label, want)
		}
	}
}
