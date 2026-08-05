package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// methodTypeParamsIRCases pin the second monomorphisation axis (#6007's last
// slice): a method may declare its OWN type parameter, independent of the
// receiver's — `function (b: Box[T]) map[U](f: (T) => U): Box[U]`.
//
// monomorphize_structs used to clone methods on exactly one axis, the struct
// instantiation, and clone_struct_method substitutes with the STRUCT's type
// params explicitly (a synthesised method like default() has none of its own).
// So cloning `Box[i32]` substituted `T` and left `U` alone, the clone's return
// spelling stayed `Box[U]`, and mg_ty — which has no notion of which names are
// type parameters — recorded an instantiation keyed on the parameter NAME. The
// worklist then emitted a `Box__U` whose field type was unbound and whose `get`
// never resolved: `call to unknown symbol Box__U.get`, and since #3457 deleted
// the AST emitters that is a hard error, not a fallback.
//
// retype_method_inst now binds the method's parameter from the binding's
// already-mangled annotation and rewrites the call to a per-instantiation
// clone; phase 2 emits one clone per (struct instantiation × method type arg)
// and never emits the method generically, so no `Box__U` is minted.
//
// The corpus fixture `generic_method_typeparams` covers the `Box[U]` return at
// one receiver instantiation. These cases add what it does not: the bare-return
// shape (`into[U](): U`), two distinct receiver instantiations, and a plain
// non-generic method alongside so the ordinary clone_struct_method path is
// still exercised. Every answer is <= 125 so the wasm exit code carries it.
const methodTypeParamsIRPrelude = `struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) into[U](f: (T) => U): U { return f(b.v); }
function (b: Box[T]) get(): T { return b.v; }
function dbl(x: i32): i32 { return x * 2; }
function is_big(x: i32): boolean { return x > 100; }
function width(s: string): i32 { return s.len(); }
function tag(x: i32): string { return "aa"; }
`

var methodTypeParamsIRCases = []struct {
	name string
	main string
	want int
}{
	// The fixture's shape, reduced: U = T, so the clone returns the receiver's
	// own instantiation. 7 -> 14.
	{"map-same-type", `var b: Box[i32] = Box { v: 7 }; var d: Box[i32] = b.map(dbl); return d.get();`, 14},
	// U differs from T: `Box[i32] -> Box[boolean]`, so the method clone's
	// return instantiation is NOT the receiver's — the case that made
	// retargeting bare Self literals to the receiver's clone wrong.
	{"map-other-type", `var b: Box[i32] = Box { v: 7 }; var g: Box[boolean] = b.map(is_big); if (g.get()) { return 1; } return 9;`, 9},
	// Both instantiations of the SAME method on the SAME receiver clone: two
	// clones must coexist (map__i32 and map__boolean on Box__i32).
	{"map-two-args", `var b: Box[i32] = Box { v: 7 }; var d: Box[i32] = b.map(dbl); var g: Box[boolean] = b.map(is_big); var r: i32 = d.get(); if (!g.get()) { r = r + 100; } return r;`, 114},
	// Bare-return shape: the method's parameter IS the whole return type, so it
	// binds from the annotation directly rather than through a struct arg.
	{"into-scalar", `var b: Box[i32] = Box { v: 5 }; var s: string = b.into(tag); return s.len() + 7;`, 9},
	// A second RECEIVER instantiation: Box__string as well as Box__i32, each
	// with its own method clone.
	{"two-receivers", `var a: Box[i32] = Box { v: 4 }; var s: Box[string] = Box { v: "xyz" }; var d: Box[i32] = a.map(dbl); var w: i32 = s.into(width); return d.get() + w;`, 11},
	// The plain non-generic method still goes through clone_struct_method.
	{"plain-method", `var b: Box[string] = Box { v: "hello" }; return b.get().len();`, 5},
}

func methodTypeParamsIRSrc(mainBody string) string {
	return methodTypeParamsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMethodTypeParamsIRX86_64 pins the x86-64 IR path: each case must
// route through the IR path (not merely compile) and return the oracle value.
func TestSelfHostMethodTypeParamsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range methodTypeParamsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(methodTypeParamsIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostMethodTypeParamsIRWasm is the wasm-IR mirror. The pass being
// fixed is in the shared frontend (parser.fern's monomorphiser), so both
// backends failed identically before it — which is why both are pinned.
func TestSelfHostMethodTypeParamsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host method-type-params wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range methodTypeParamsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(methodTypeParamsIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watPath)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
