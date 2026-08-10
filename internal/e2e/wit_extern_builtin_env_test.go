package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExternImportWithBuiltinEnvArgsViaCLI is the parity gate for the
// world-driven composer's coverage of the list-returning CLI capabilities
// (docs/WIT-BRING-YOUR-OWN.md). When a program declares any `@import` extern
// the CLI routes the WHOLE module through ComposeFromWorldAuto, which wires
// every import — extern and built-in alike — against the embedded `fern`
// world. Before `wasi:cli/environment` was added to that world, an extern
// program that also called `args()` / `env()` failed to compose with
// "core imports interface \"wasi:cli/environment@0.2.0\" not declared by the
// world". This pins that those built-ins now lower correctly through the
// world path (KindMemRealloc, list<string> / list<tuple<string,string>>),
// matching the bespoke registry lowering byte-for-result.
//
// The extern is get-random-bytes (a composite-result import the legacy
// registry doesn't recognise, so it forces the world path); the built-ins
// are args() and env(), both list-returning.
func TestExternImportWithBuiltinEnvArgsViaCLI(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}

	const want = "env-args-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];

function main(): i32 {
	var a: string[] = args();
	var g: string = "MISS";
	match (env("GREETING")) { Some(v) => { g = v; }, None => {} }
	var b: u8[] = rand_bytes(8 as u64);
	if (b.len() == 8 && a.len() == 3 && g == "` + want + `") { write(g); } else { write("bad"); }
	return 0;
}`
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	compPath := filepath.Join(dir, "prog.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", compPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	// Two extra argv entries (argv[0] is the component name → len 3) and the
	// GREETING env var exercise get-arguments + get-environment respectively.
	out, err := exec.Command(wasmtime, "run", "--env", "GREETING="+want, compPath, "alpha", "beta").CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
