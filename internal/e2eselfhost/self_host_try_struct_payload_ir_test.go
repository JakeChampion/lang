package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An UNANNOTATED `var u = f()?` whose success payload is a STRUCT (or nominal
// ENUM) now lowers on the self-host IR path. The try-operator bind already
// recovered a TUPLE payload's element tags (so `u.0` / `u.1` read) via
// try_opt_type, but a struct / enum payload left the slot untyped — so `u.field`
// / `u.method()` / `match (u)` in the body failed to lower and the whole module
// dropped to the legacy AST emitter. The fix carries the payload's struct/enum
// name onto the slot (mark_struct_type), exactly what an explicit
// `var u: User = f()?` annotation already did (the annotated form always worked;
// only the inferred one bailed). Found by differential probing; each case is
// oracle-checked and routing-pinned "ir".
var tryStructPayloadIRCases = []struct {
	name string
	src  string
}{
	// The minimal case: a Result[Struct, E] try, read a field of the payload → 42.
	{"result_struct", `struct User { id: i32 }
function find(n: i32): Result[User, string] { if (n > 0) { return Ok(User { id: n }); } return Err("bad"); }
function getid(n: i32): Result[i32, string] { var u = find(n)?; return Ok(u.id); }
function main(): i32 { match (getid(42)) { Ok(id) => { return id; }, Err(e) => { return 0; } } }`},
	// An Option[Struct] try, calling a METHOD on the payload → 21 * 2 = 42.
	{"option_struct_method", `struct User { id: i32 }
function (u: User) doubled(): i32 { return u.id * 2; }
function find(n: i32): Option[User] { if (n > 0) { return Some(User { id: n }); } return None; }
function getid(n: i32): Option[i32] { var u = find(n)?; return Some(u.doubled()); }
function main(): i32 { match (getid(21)) { Some(v) => { return v; }, None => { return 0; } } }`},
	// A nominal-ENUM payload, matched in the body → 42.
	{"result_enum_match", `enum Color { Red, Green, Blue }
function pick(n: i32): Result[Color, string] { if (n > 0) { return Ok(Color.Green); } return Err("bad"); }
function go(n: i32): Result[i32, string] {
    var c = pick(n)?;
    match (c) { Color.Green => { return Ok(42); }, _ => { return Ok(0); } }
}
function main(): i32 { match (go(5)) { Ok(v) => { return v; }, Err(e) => { return 0; } } }`},
	// RC stress: a struct payload with a STRING field, unwrapped via `?` in a
	// 50-iteration loop — the leak-only payload must not over-release. Each round
	// contributes id + name.len() = i + 1; sum(1..50) + 50 = 1275 + 50 = 1325;
	// 1325 % 256 = 45.
	{"rc_stress", `struct User { id: i32, name: string }
function find(n: i32): Result[User, string] { if (n > 0) { return Ok(User { id: n, name: "u" }); } return Err("x"); }
function getid(n: i32): Result[i32, string] { var u = find(n)?; return Ok(u.id + u.name.len()); }
function main(): i32 {
    var acc = 0; var i = 1;
    while (i <= 50) { match (getid(i)) { Ok(v) => { acc = acc + v; }, Err(e) => {} } i = i + 1; }
    return acc % 256;
}`},
}

// The x86-64 leg.
func TestSelfHostTryStructPayloadIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "tsp")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range tryStructPayloadIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "tsp_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "tsp_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}

// The wasm leg: the fix lives in shared irlower.fern, so the wasm IR backend types
// the try-payload slot the same way.
func TestSelfHostTryStructPayloadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping try-struct-payload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range tryStructPayloadIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "wtsp_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s wasm = %d, want %d (native oracle)", tc.name, got, want)
			}
		})
	}
}
