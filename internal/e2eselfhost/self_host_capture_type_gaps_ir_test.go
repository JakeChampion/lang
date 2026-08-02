package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The last three constructs of the enumerated mode-0 decline set (#3457), each of
// which sent its whole module to the legacy AST emitter.
//
// Like the group in self_host_mode0_gaps_ir_test.go, none is a missing FEATURE —
// each is a piece of type information the closure-lift or the map lowering fails
// to carry one step further than it already does:
//
//   - a method's escaping lambda capturing the RECEIVER. lambda_captures built its
//     "enclosing local" set from fd's params and body bindings and omitted the
//     receiver, so `a` in the lambda was not a capture at all. `caps` came back
//     EMPTY, so the NO-capture lift hoisted the body to a `<fd>$wrapN` trampoline
//     in which the receiver name is unbound, and the module bailed on the wrapper.
//   - a capture whose local is initialised from a field access / call / index.
//     cap_type_expr knew literals, idents and arithmetic only, so `var b = a.base`
//     resolved "" and cap_slot_ok declined the lift. cap_type_in_stmts already did
//     exactly these resolutions for a for-in iter and a match scrutinee; the plain
//     `var` init arm simply never did, which is why ANNOTATING the local was the
//     only way through.
//   - a Map with STRUCT values. `var p = m.get_or(k, d)` had no struct type, so
//     `p.field` bailed. Three sites carry V now: the get_or read (expr_struct_type),
//     the unannotated `map_new(…).insert(k, P { … })` chain, and the separate
//     `m = m.insert(k, P { … })` assignment (refine_map_struct_val).
//
// Every case asserts the `-decide` route AND the answer, because a regression here
// is silent: the AST emitter computes all of these correctly, so only the route
// shows it. The map cases have no interpreter oracle (`map_new_i32` is E001
// natively — self-host dialect), so their exit codes are stated, not derived.
func TestSelfHostCaptureTypeGapsIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, rerr := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), src, 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// The receiver as a capture. The param and free-function forms are the
		// controls that already lowered, which is what isolates the receiver.
		{"escaping-captures-receiver", "struct A { base: i32 }\nfunction (a: A) make(): (i32) => i32 { return function(x: i32): i32 { return x + a.base; }; }\nfunction main(): i32 { var a = A { base: 100 }; var f = a.make(); return f(5); }", 105},
		{"escaping-captures-method-param-control", "struct A { base: i32 }\nfunction (a: A) make(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; }\nfunction main(): i32 { var a = A { base: 1 }; var f = a.make(100); return f(5); }", 105},
		{"escaping-captures-free-param-control", "function make(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; }\nfunction main(): i32 { var f = make(100); return f(5); }", 105},

		// A capture local the lift could not type. The annotated form is the
		// control that always worked; the arithmetic-over-i32 form is the one
		// cap_type_expr already covered.
		{"capture-local-from-field", "struct A { base: i32 }\nfunction make(a: A): (i32) => i32 { var b = a.base; return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var a = A { base: 100 }; var f = make(a); return f(5); }", 105},
		{"capture-local-from-field-annotated-control", "struct A { base: i32 }\nfunction make(a: A): (i32) => i32 { var b: i32 = a.base; return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var a = A { base: 100 }; var f = make(a); return f(5); }", 105},
		{"capture-local-from-field-arith", "struct A { base: i32 }\nfunction make(a: A): (i32) => i32 { var b = a.base + 0; return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var a = A { base: 100 }; var f = make(a); return f(5); }", 105},
		{"capture-local-from-field-in-method", "struct A { base: i32 }\nfunction (a: A) make(): (i32) => i32 { var b = a.base; return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var a = A { base: 100 }; var f = a.make(); return f(5); }", 105},
		{"capture-local-from-call", "function base(): i32 { return 100; }\nfunction make(): (i32) => i32 { var b = base(); return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var f = make(); return f(5); }", 105},
		{"capture-local-from-index", "function make(xs: i32[]): (i32) => i32 { var b = xs[1]; return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var f = make([7, 100]); return f(5); }", 105},
		{"capture-local-from-arith-control", "function make(n: i32): (i32) => i32 { var b = n + 1; return function(x: i32): i32 { return x + b; }; }\nfunction main(): i32 { var f = make(99); return f(5); }", 105},

		// Map with struct values, in the three binding shapes. The i32-valued and
		// string-valued maps are the controls that always lowered.
		{"map-struct-val-separate-insert", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var m = map_new_i32(8); m = m.insert(1, P { x: 40, y: 2 }); var p = m.get_or(1, P { x: 0, y: 0 }); return p.x + p.y; }", 42},
		{"map-struct-val-insert-chain", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var m = map_new_i32(8).insert(1, P { x: 40, y: 2 }); var p = m.get_or(1, P { x: 0, y: 0 }); return p.x + p.y; }", 42},
		{"map-struct-val-annotated-control", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var m: Map[i32, P] = map_new_i32(8); m = m.insert(1, P { x: 40, y: 2 }); var p: P = m.get_or(1, P { x: 0, y: 0 }); return p.x + p.y; }", 42},
		{"map-i32-val-control", "function main(): i32 { var m = map_new_i32(8); m = m.insert(1, 42); return m.get_or(1, 0); }", 42},
		{"map-string-val-control", "function main(): i32 { var m = map_new_i32(8); m = m.insert(1, \"hi\"); var s = m.get_or(1, \"\"); return s.len() + 40; }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the construct is back on the AST emitter, which computes it correctly, so only the route shows it", tc.name, route)
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
