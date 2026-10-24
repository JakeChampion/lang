package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostCaptureTypeGapsIR(t *testing.T) {
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
		{"escaping-captures-receiver", "struct A { base: i32 }\nfunction (a: A) make(): (i32) => i32 { return (x: i32): i32 => { return x + a.base; }; }\nfunction main(): i32 { let a = A { base: 100 }; let f = a.make(); return f(5); }", 105},
		{"escaping-captures-method-param-control", "struct A { base: i32 }\nfunction (a: A) make(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; }\nfunction main(): i32 { let a = A { base: 1 }; let f = a.make(100); return f(5); }", 105},
		{"escaping-captures-free-param-control", "function make(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; }\nfunction main(): i32 { let f = make(100); return f(5); }", 105},

		{"capture-local-from-field", "struct A { base: i32 }\nfunction make(a: A): (i32) => i32 { let b = a.base; return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let a = A { base: 100 }; let f = make(a); return f(5); }", 105},
		{"capture-local-from-field-annotated-control", "struct A { base: i32 }\nfunction make(a: A): (i32) => i32 { let b: i32 = a.base; return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let a = A { base: 100 }; let f = make(a); return f(5); }", 105},
		{"capture-local-from-field-arith", "struct A { base: i32 }\nfunction make(a: A): (i32) => i32 { let b = a.base + 0; return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let a = A { base: 100 }; let f = make(a); return f(5); }", 105},
		{"capture-local-from-field-in-method", "struct A { base: i32 }\nfunction (a: A) make(): (i32) => i32 { let b = a.base; return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let a = A { base: 100 }; let f = a.make(); return f(5); }", 105},
		{"capture-local-from-call", "function base(): i32 { return 100; }\nfunction make(): (i32) => i32 { let b = base(); return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let f = make(); return f(5); }", 105},
		{"capture-local-from-index", "function make(xs: i32[]): (i32) => i32 { let b = xs[1]; return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let f = make([7, 100]); return f(5); }", 105},
		{"capture-local-from-arith-control", "function make(n: i32): (i32) => i32 { let b = n + 1; return (x: i32): i32 => { return x + b; }; }\nfunction main(): i32 { let f = make(99); return f(5); }", 105},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the construct no longer lowers, and the value alone would not show it", tc.name, route)
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
