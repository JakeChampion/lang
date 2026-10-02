package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostIOAllBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range []struct {
			name, source string
			input        []byte
		}{
			{"borrowed", e2eharness.IOAllBytesProgram, e2eharness.IOAllBytesInput()},
			{"stdin", e2eharness.IOStdinBytesProgram("io.read_all_stdin_bytes()", false), e2eharness.IOAllBytesInput()},
			{"dash", e2eharness.IOStdinBytesProgram(`io.read_input_bytes("-")`, false), e2eharness.IOAllBytesInput()},
			{"empty", e2eharness.IOStdinBytesProgram(`io.read_input_bytes("")`, true), nil},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOfStdin(t, tc.source, target, tc.input, "FERN_SEM_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit = %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
