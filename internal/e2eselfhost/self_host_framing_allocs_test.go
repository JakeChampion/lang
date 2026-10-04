package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// #9853's framing-path allocation gate through the self-host compiler:
// what parsing a hello request and serializing its reply allocate, per
// request, on each target. docs/NET-P0-MESSAGE-LAYER-PLAN.md takes these
// to zero; each slice lowers a pin. The Go compiler's twin is
// TestFramingAllocs.
func TestSelfHostFramingAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.FramingAllocsSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		want   e2eharness.FramingAllocs
	}{
		{"x86-64-linux", e2eharness.FramingAllocs{Parse: 42, Serialize: 4}},
		{"arm64-linux", e2eharness.FramingAllocs{Parse: 42, Serialize: 4}},
		{"wasm32-wasi", e2eharness.FramingAllocs{Parse: 42, Serialize: 4}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			out, code := cli.exitOfFile(t, src, tc.target, nil, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("framing probe exited %d:\n%s", code, out)
			}
			e2eharness.CheckFramingAllocs(t, out, tc.want)
		})
	}
}
