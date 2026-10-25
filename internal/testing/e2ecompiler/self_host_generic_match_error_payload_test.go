package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const genericMatchErrorPayloadSource = `import "std/i32";
enum CodecFault { Broken(string), Other }
enum Failure[E] { Wrapped(CodecFault), User(E), Budget }
struct Kept[S] { value: S }

function source(mode: i32): Result[i32, CodecFault] {
    if (mode == 0) { return Err(Broken("source-" + mode.to_string())); }
    return Ok(mode);
}

function flow[S,E](mode: i32, value: S, consume: (S) => Result[S,E]): Result[Kept[S], Failure[E]] {
    let n = match (source(mode)) { Ok(v) => v, Err(error) => { return Err(Wrapped(error)); } };
    let next = match (consume(value)) { Ok(v) => v, Err(error) => { return Err(User(error)); } };
    if (n < 0) { return Err(Budget); }
    return Ok(Kept[S] { value: next });
}

function main(): i32 {
    let accept = (value: string): Result[string,string] => Ok(value + "-ok");
    let reject = (value: string): Result[string,string] => Err(value + "-error");
    let bad_source = match (flow(0, "seed", accept)) { Err(Wrapped(Broken(text))) => text == "source-0", _ => false };
    let bad_consumer = match (flow(1, "seed", reject)) { Err(User(text)) => text == "seed-error", _ => false };
    let good = match (flow(2, "seed", accept)) { Ok(value) => value.value == "seed-ok", _ => false };
    let integer = (value: i32): Result[i32,i32] => Ok(value + 1);
    let another = match (flow(2, 41, integer)) { Ok(value) => value.value == 42, _ => false };
    if (!bad_source || !bad_consumer || !good || !another) { return 1; }
    return 42;
}
`

func TestSelfHostGenericMatchErrorPayloadX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if got := interpExit(t, buildLangBinForInterp(t), genericMatchErrorPayloadSource); got != 42 {
		t.Fatalf("interpreter = %d, want 42", got)
	}
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", genericMatchErrorPayloadSource)); code != 42 {
		t.Fatalf("native exit %d, want 42", code)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", genericMatchErrorPayloadSource, "FERN_SANITIZE=1"))
	stderr, code := runWithStdin(t, cli.runner, bin, nil)
	if code != 42 {
		t.Fatalf("native census exit %d, want 42\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostGenericMatchErrorPayloadWasm(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", genericMatchErrorPayloadSource)); code != 42 {
		t.Fatalf("wasm exit %d, want 42", code)
	}
	path := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(path, []byte(cli.emit(t, "wasm32-wasi", genericMatchErrorPayloadSource, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, code := runWasmCensus(t, path)
	if code != 42 {
		t.Fatalf("wasm census exit %d, want 42\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
}
