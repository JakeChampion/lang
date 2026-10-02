package e2eselfhost

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A struct-array local reassigned from a producer call (#10360). The credit
// that frees a struct array's element boxes refused every reassignment but the
// self-append, so `pending = mk(1)` released the buffer alone and stranded each
// element box. A producer rebind now keeps the credit, and the rebind store
// frees the superseded array whole. Answers are the interpreter's.

// leakRow is one program held to the census. balanced false holds it to the
// sanitizer only: a leak is allowed, any other report is not.
type leakRow struct {
	name, src string
	balanced  bool
}

const structArrRebindInst = `struct Inst { name: string, depth: i32 }
function mk(n: i32): Inst[] {
    let out: Inst[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(Inst { name: "g" + "", depth: i }); i = i + 1; }
    return out;
}
`

var structArrProducerRebindRows = []leakRow{
	{"issue", `struct Inst { name: string, depth: i32 }
function mk(n: i32): Inst[] {
    let out: Inst[] = [];
    out = out.append(Inst { name: "g" + "", depth: n });
    return out;
}
function main(): i32 {
    let pending: Inst[] = [];
    pending = mk(1);
    return pending.len();
}
`, true},
	{"producer_seed", structArrRebindInst + `function main(): i32 {
    let pending: Inst[] = mk(2);
    pending = mk(3);
    return pending.len() + pending[2].depth;
}
`, true},
	{"literal_seed", structArrRebindInst + `function main(): i32 {
    let pending: Inst[] = [Inst { name: "q" + "", depth: 7 }];
    let t: i32 = pending[0].depth;
    pending = mk(2);
    return t + pending.len();
}
`, true},
	{"loop_with_append", structArrRebindInst + `function main(): i32 {
    let pending: Inst[] = [];
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 6) {
        pending = mk(r);
        for p in pending { t = t + p.depth; }
        if (r % 2 == 0) { pending = pending.append(Inst { name: "h" + "", depth: 1 }); }
        t = t + pending.len();
        r = r + 1;
    }
    return t;
}
`, true},
	{"array_field_elem", `struct Node { name: string, kids: i32[] }
function mk(n: i32): Node[] {
    let out: Node[] = [];
    out = out.append(Node { name: "g" + "", kids: [n, n + 1] });
    return out;
}
function main(): i32 {
    let pending: Node[] = [];
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 4) { pending = mk(r); t = t + pending[0].kids[1]; r = r + 1; }
    return t + pending.len();
}
`, true},
	// An element bound out of the old array keeps it from the credit.
	{"bound_elem", structArrRebindInst + `function main(): i32 {
    let pending: Inst[] = [];
    pending = mk(3);
    let keep: Inst = pending[1];
    pending = mk(4);
    return keep.depth + pending[3].depth;
}
`, false},
}

func runLeakRowsX86_64(t *testing.T, rows []leakRow) {
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range rows {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, want, stderr)
			}
			if tc.balanced {
				assertBalancedCensus(t, stderr)
			}
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != want || forArrStructSanitizerFault(stderr, tc.balanced) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, want, stderr)
			}
		})
	}
}

func runLeakRowsArm64(t *testing.T, rows []leakRow) {
	armgcc, qemu := arm64Tooling(t)
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range rows {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		t.Run(tc.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), tc.name, string(asm)))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Fatalf("exit = %d, want %d\n%s", code, want, eb.String())
			}
			if tc.balanced {
				assertBalancedCensus(t, eb.String())
			}
		})
	}
}

func runLeakRowsWasm(t *testing.T, rows []leakRow) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Fatal("wasmtime not on PATH")
	}
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range rows {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != want {
				t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
			}
			if tc.balanced {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}

func TestSelfHostStructArrProducerRebindX86_64(t *testing.T) {
	runLeakRowsX86_64(t, structArrProducerRebindRows)
}

func TestSelfHostStructArrProducerRebindArm64(t *testing.T) {
	runLeakRowsArm64(t, structArrProducerRebindRows)
}

func TestSelfHostStructArrProducerRebindWasm(t *testing.T) {
	runLeakRowsWasm(t, structArrProducerRebindRows)
}
