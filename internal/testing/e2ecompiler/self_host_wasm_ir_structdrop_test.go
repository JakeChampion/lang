package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIRStructDropEmitted retains constant nested structs as static
// controls and exercises nested release-helper calls with runtime payloads.
// Every module must define the release helper and execute the original result.
func TestSelfHostWasmIRStructDropEmitted(t *testing.T) {
	boxedProbes(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IR struct-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		// drop is the struct type whose `$__sem_release_<drop>` definition the
		// WAT must contain.
		drop string
		heap bool
		want int
		src  string
	}{
		// Constant Box{Point} is passed to bx() from static aggregate data.
		{"nested-struct", "Box", false, 42,
			`struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { let b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }`},
		// Keep the original three-deep static construction and result.
		{"deep-nested", "Outer", false, 105,
			`struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { let o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }`},
		// args includes the program name, so probe receives 1 and reconstructs
		// the original payloads without constant aggregate lowering.
		{"nested-runtime", "Box", true, 42,
			`struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } @noinline function probe(n: i32): i32 { let b = Box { p: Point { x: n + 29, y: 12 } }; return bx(b); } function main(): i32 { return probe(args().len()); }`},
		{"deep-runtime", "Outer", true, 105,
			`struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } @noinline function probe(n: i32): i32 { let o = Outer { mid: Mid { inner: Inner { v: n + 99 }, n: 5 } }; return f(o); } function main(): i32 { return probe(args().len()); }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			// Scope runtime allocation/release checks to probe, excluding argv
			// cleanup in main. The original constant programs use static data.
			call := []byte("call $__sem_release_" + tc.drop)
			def := []byte("(func $__sem_release_" + tc.drop)
			name := "$main "
			if tc.heap {
				name = "$probe "
			}
			bodies := wasmFuncBodies(string(wat), name)
			if len(bodies) != 1 {
				t.Fatalf("want one %s body, got %d", name, len(bodies))
			}
			body := bodies[0]
			if strings.Contains(body, string(call)) != tc.heap {
				t.Errorf("release call in %s: want %t\n%s", name, tc.heap, body)
			}
			if strings.Contains(body, "call $__fern_str_box") != tc.heap {
				t.Errorf("allocation in %s: want %t\n%s", name, tc.heap, body)
			}
			if !tc.heap && !strings.Contains(body, "global.get $__cagg_base") {
				t.Errorf("no static aggregate in %s\n%s", name, body)
			}
			if !bytes.Contains(wat, def) {
				t.Fatalf("WAT never defines $__sem_release_%s\n%s", tc.drop, wat)
			}
			watFile := filepath.Join(dir, "drop_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("struct-drop program %q exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.want, wat)
			}
		})
	}
}
