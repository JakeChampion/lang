package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Two closure-lift shapes from the mode-0 decline set (#3457). Like the group in
// self_host_mode0_gaps_ir_test.go, neither is a missing FEATURE — each is a piece
// of type information the closure-lift has to carry one step further:
//
//   - a method's escaping lambda capturing the RECEIVER. The receiver is an
//     enclosing local like the params, so `a` in the lambda is a capture; lifted
//     as capture-free, the body would land in a `<fd>$wrapN` trampoline in which
//     the receiver name is unbound.
//   - a capture whose UNANNOTATED local is initialised from a field access /
//     call / index (`let b = a.base`). The lift has to type the capture from the
//     initialiser, as it does for a for-in iter and a match scrutinee.
//
// Every case asserts the `-decide` route AND the answer: a regression is a
// refused module, and the route names it.
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
		// The receiver as a capture. The param and free-function forms are the
		// controls that already lowered, which is what isolates the receiver.
		{"escaping-captures-receiver", "struct A { base: i32 }\nfunction (a: A) make(): (i32) => i32 { return (x: i32): i32 => { return x + a.base; }; }\nfunction main(): i32 { let a = A { base: 100 }; let f = a.make(); return f(5); }", 105},
		{"escaping-captures-method-param-control", "struct A { base: i32 }\nfunction (a: A) make(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; }\nfunction main(): i32 { let a = A { base: 1 }; let f = a.make(100); return f(5); }", 105},
		{"escaping-captures-free-param-control", "function make(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; }\nfunction main(): i32 { let f = make(100); return f(5); }", 105},

		// A capture local typed from its unannotated initialiser. The annotated
		// form and the arithmetic-over-i32 form are the controls that always
		// lowered.
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
