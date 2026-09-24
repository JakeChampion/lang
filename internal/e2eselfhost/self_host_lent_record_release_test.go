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
    var r: Rec = Rec { n: i, xs: ["first-name-" + i.to_string(), "second-name-" + i.to_string()] };
    var ys: string[] = keep_field(r);
    keep_field(r);
    var zs: string[] = r.names();
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
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        n = n + use_rec(i) + measure(shape(i)) + weigh(tag(i));
        i = i + 1;
    }
    return n % 101;
}
`

// Interpreter-confirmed.
const lentRecordWant = 44

var lentRecordLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
	{"ast_main", "FERN_SEM_IR_SKIP=main"},
	{"ast_caller", "FERN_SEM_IR_SKIP=use_rec"},
}

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
	for _, lw := range lentRecordLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != lentRecordWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, lentRecordWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostLentRecordReleaseSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	for _, lw := range lentRecordLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != lentRecordWant || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, lentRecordWant, stderr)
			}
		})
	}
}

func TestSelfHostLentRecordReleaseArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	for _, lw := range lentRecordLowerings {
		t.Run(lw.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", lw.env))
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
		})
	}
}

func TestSelfHostLentRecordReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm lent-record release")
	}
	cli := buildSelfHostCLI(t)
	src := writeLentRecordSrc(t)
	for _, lw := range lentRecordLowerings {
		t.Run(lw.name, func(t *testing.T) {
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWasmCensus(t, wat)
			if exit != lentRecordWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, lentRecordWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
