package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A record lent to a callee that hands back its string[] field, and fresh enum
// results passed straight into a helper, leaked on the AST lowering and on the
// mixed pipeline where only some functions are produced (#9187). The forwarder
// call marked the record's field unsafe although the forwarding return
// retains it, so the record lost its deep drop; a fresh enum argument had no
// stash; a scalar enum result read back through caller_sigs had no row; and a
// parameter whose match returned a scalar binding read as escaping.
const lentRecordSrc = `import "std/i32";
struct Rec { n: i32, xs: string[] }
function keep_field(r: Rec): string[] { return r.xs; }
function (r: Rec) names(): string[] { return r.xs; }
function use_rec(i: i32): i32 {
    let r: Rec = Rec { n: i, xs: ["first-name-" + i.to_string(), "second-name-" + i.to_string()] };
    let ys: string[] = keep_field(r);
    keep_field(r);
    let zs: string[] = r.names();
    return ys.len() + zs.len() + r.n % 3;
}
enum Shape { Pair(i32, i32[]), Nope }
function shape(k: i32): Shape { return Pair(k, [k, k]); }
function measure(s: Shape): i32 {
    match (s) { Pair(a, xs) => { return a + xs.len(); }, Nope => { return 0; } }
}
enum Tag { Small(i32), Big(i32, i32) }
function tag(k: i32): Tag {
    if (k % 2 == 0) { return Small(k); }
    return Big(k, 1);
}
function weigh(t: Tag): i32 {
    match (t) { Small(a) => { return a; }, Big(a, b) => { return a + b; } }
}
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        n = n + use_rec(i) + measure(shape(i)) + weigh(tag(i));
        i = i + 1;
    }
    return n % 101;
}
`

// Interpreter-confirmed.
const lentRecordWant = 44

func writeLentRecordSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lent_record.fern")
	if err := os.WriteFile(path, []byte(lentRecordSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostLentRecordReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != lentRecordWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, lentRecordWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostLentRecordReleaseSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	bin := cli.x86Binary(t, src, "FERN_SANITIZE=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != lentRecordWant || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, lentRecordWant, stderr)
	}
}

func TestSelfHostLentRecordReleaseArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "lent_record", string(asm)))
	var eb strings.Builder
	cmd.Stderr = &eb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != lentRecordWant {
		t.Fatalf("exit = %d, want %d\n%s", code, lentRecordWant, eb.String())
	}
	assertBalancedCensus(t, eb.String())
}

func TestSelfHostLentRecordReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm lent-record release")
	}
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1")
	stderr, exit := runWasmCensus(t, wat)
	if exit != lentRecordWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, lentRecordWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
