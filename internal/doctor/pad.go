package doctor

import (
	"fmt"

	"github.com/sroberts/tally/internal/slots"
)

// PadProtocol reports a MacroPad on path that answered ping with hello.
// Its firmware must speak the serial protocol version tally sends
// (slots.Version); any other version misreads every frame.
func PadProtocol(path, fw string, v int) Result {
	r := Result{Name: "pad", Level: OK, Detail: fmt.Sprintf("%s answered: firmware %s, protocol v%d", path, fw, v)}
	switch {
	case v < slots.Version:
		r.Level = Warn
		r.Detail += fmt.Sprintf(", but tally sends v%d", slots.Version)
		r.Fix = "update the pad's firmware: copy vibe-macropad's firmware/*.py to CIRCUITPY and replug the pad"
	case v > slots.Version:
		r.Level = Warn
		r.Detail += fmt.Sprintf(", but tally sends v%d", slots.Version)
		r.Fix = "update tally: go install github.com/sroberts/tally@latest, then tally install-agent"
	}
	return r
}
