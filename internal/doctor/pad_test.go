package doctor

import (
	"strings"
	"testing"

	"github.com/sroberts/tally/internal/slots"
)

func TestPadProtocol(t *testing.T) {
	if r := PadProtocol("/dev/cu.usbmodem1", "0.4.0", slots.Version); r.Level != OK || !strings.Contains(r.Detail, "firmware 0.4.0, protocol v2") {
		t.Errorf("same version: %+v", r)
	}
	r := PadProtocol("/dev/cu.usbmodem1", "0.1.0", slots.Version-1)
	if r.Level != Warn || !strings.Contains(r.Detail, "tally sends v2") || !strings.Contains(r.Fix, "update the pad's firmware") {
		t.Errorf("older firmware: %+v", r)
	}
	r = PadProtocol("/dev/cu.usbmodem1", "9.0.0", slots.Version+1)
	if r.Level != Warn || !strings.Contains(r.Fix, "update tally") {
		t.Errorf("newer firmware: %+v", r)
	}
}
