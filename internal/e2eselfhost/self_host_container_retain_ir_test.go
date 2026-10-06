package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fresh literal stored into a container is moved rather than retained, and a
// module that reads the RC counters while allocating nothing an op reports
// still links the counter's runtime.
func TestSelfHostContainerRetainIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A FRESH literal element is not a local: it is moved, not retained, so
		// the deep-free paths that own fresh elements stay correct.
		{"fresh-element-not-retained", `function main(): i32 { let t = ([1, 2, 3], 99); return t.0[2] + 4; }`, 7},
		// The RC counters are read by a module that allocates NOTHING an op
		// reports (a scalar-capture closure). rc_runtime_helpers depends on the heap
		// gate, so without pulling it in for a counter read the emitted core
		// called a function it never defined.
		{"scalar-closure-reads-counter", `function main(): i32 { let n: i32 = 5; let f = (x: i32): i32 => { return x + n; }; return f(37) + __rc_underflow_count(); }`, 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the case is not exercising the IR path it is about", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, "run", watPath)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
