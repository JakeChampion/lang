package e2eselfhost

import (
	"os/exec"
	"testing"
)

// A builtin union's literal takes its type arguments from the destination it
// is written against, and a method's own type parameter is settled by that
// very argument: `and[U](other: Result[U, E])` on a `Result[i32, string]`
// hands `Ok("v")` the destination `Result[U, string]`, which names the
// variant without saying what it holds. The payload's checked type binds U
// (semsource.settled_union), for Ok and Err as for Some.
//
// The string payload is pinned here rather than in the production rows: that
// harness's oracle is the AST lowering, which misreads this shape through the
// erased U (#10014). The oracle here is the interpreter — every `acc` below
// was confirmed against `bin/fern -interp` — and the churn gate's own asserts:
// produced whole, a flat heap over 1000 rounds, no rc underflow.
var builtinUnionPayloadPrograms = []mapChurnProgram{
	// `build(n)` is 1 + digits(n) for the Ok receiver's payload plus 1 for the
	// Err receiver's fallback: 390 over 0..99 and 4890 over 0..999.
	{"result-and-string-payload", `import "std/result";
import "core/cmp";
function build(n: i32): i32 {
    var r: Result[i32, string] = Ok(n);
    var s: Result[string, string] = r.and(Ok("v" + n.to_string()));
    var e: Result[i32, string] = Err("no" + n.to_string());
    var f: Result[string, string] = e.and(Ok("zz"));
    return s.unwrap_or("").len() + f.unwrap_or("q").len();
}
` + mapChurnMain(5280)},
	// The same settle through Option: `and[U](other: Option[U])` was the one
	// shape the removed `some_of` special case covered.
	{"option-and-string-payload", `import "std/option";
import "core/cmp";
function build(n: i32): i32 {
    var o: Option[i32] = Some(n);
    var s: Option[string] = o.and(Some("w" + n.to_string()));
    var z: Option[i32] = None;
    var t: Option[string] = z.and(Some("zz"));
    return s.unwrap_or("").len() + t.unwrap_or("q").len();
}
` + mapChurnMain(5280)},
}

func TestSelfHostBuiltinUnionPayloadX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	runMapChurnTyped(t, runner, "x86-64-linux", builtinUnionPayloadPrograms)
}

func TestSelfHostBuiltinUnionPayloadArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	runMapChurnTyped(t, []string{qemu}, "arm64-linux", builtinUnionPayloadPrograms)
}

func TestSelfHostBuiltinUnionPayloadWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	runMapChurnTyped(t, nil, "wasm32-wasi", builtinUnionPayloadPrograms)
}
