package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
		{"x86-64-linux", e2eharness.FramingAllocs{Parse: 0, Serialize: 0}},
		{"arm64-linux", e2eharness.FramingAllocs{Parse: 0, Serialize: 0}},
		{"wasm32-wasi", e2eharness.FramingAllocs{Parse: 0, Serialize: 0}},
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
