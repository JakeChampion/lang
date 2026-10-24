package e2ecompiler

import (
	"os/exec"
	"testing"
)

var builtinUnionPayloadPrograms = []mapChurnProgram{
	{"result-and-string-payload", `import "std/result";
import "core/cmp";
function build(n: i32): i32 {
    let r: Result[i32, string] = Ok(n);
    let s: Result[string, string] = r.and(Ok("v" + n.to_string()));
    let e: Result[i32, string] = Err("no" + n.to_string());
    let f: Result[string, string] = e.and(Ok("zz"));
    return s.unwrap_or("").len() + f.unwrap_or("q").len();
}
` + mapChurnMain(5280)},
	{"option-and-string-payload", `import "std/option";
import "core/cmp";
function build(n: i32): i32 {
    let o: Option[i32] = Some(n);
    let s: Option[string] = o.and(Some("w" + n.to_string()));
    let z: Option[i32] = None;
    let t: Option[string] = z.and(Some("zz"));
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
