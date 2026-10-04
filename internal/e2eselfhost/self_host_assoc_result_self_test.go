package e2eselfhost

import (
	"path/filepath"
	"testing"
)

// A derived associated function on a generic struct whose result wraps Self,
// `Box.from_json`, is instantiated from the `Result[Box[string], string]` it
// lands in (#10709). The impl-written forms are the conformance case
// generic_assoc_result_self, which both compilers run; the derive goes
// through `json.decode[Box[T]]`, which the native compiler does not resolve
// (#11423).
const assocResultSelfSrc = `import "std/json";

@derive(json.FromJson)
struct Box[T] { v: T }

function decoded(): Result[Box[i32], string] {
    return Box.from_json("{\"v\":7}");
}

function main(): i32 {
    let boxed: Result[Box[string], string] = Box.from_json("{\"v\":\"boxed\"}");
    match (boxed) {
        Ok(b) => { if (b.v != "boxed") { return 1; } },
        Err(_) => { return 2; }
    }
    match (decoded()) {
        Ok(d) => { if (d.v != 7) { return 3; } },
        Err(_) => { return 4; }
    }
    return 0;
}
`

func TestSelfHostAssocResultSelfFromDestination(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if exit, stderr := selfHostCLIRun(t, selfHostBin, stdlibRoot, assocResultSelfSrc, target); exit != 0 {
				t.Fatalf("exit %d, want 0\n%s", exit, stderr)
			}
		})
	}
}
