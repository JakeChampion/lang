package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fieldReadMoveCases pin #10482: a local bound from a field read of an `own`
// parameter (`let fr = st.fr`) holds no count of its own, so storing it into a
// struct or tuple literal at its last use is not a move. The construction has
// to retain it, because the parameter's exit drop releases the field. #10414's
// pass had this shape in strarr_own_node, and the gen1 compiler segfaulted
// there.
//
// balanced marks the rows whose census reads zero. The tuple-element and
// branch-rebound rows report a leak: the retain is kept and nothing gives it
// back, which is the sound direction.
var fieldReadMoveCases = []struct {
	name     string
	src      string
	balanced bool
}{
	{"struct-field-from-own-param", `struct Frame { key: string, n: i32 }
struct Acc { fr: Frame, m: i32 }
function step(n: i32, own st: Acc): Acc {
    let fr: Frame = st.fr;
    return Acc { fr: fr, m: st.m + n };
}
function main(): i32 {
    let acc: Acc = Acc { fr: Frame { key: "k" + "", n: 1 }, m: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = step(i, acc); i = i + 1; }
    return acc.m + acc.fr.key.len() + acc.fr.n;
}`, true},
	{"string-field-from-own-param", `struct Acc { key: string, m: i32 }
function step(n: i32, own st: Acc): Acc {
    let k: string = st.key;
    let i: i32 = 0;
    while (i < n) { i = i + k.len(); }
    return Acc { key: k, m: st.m + i };
}
function main(): i32 {
    let acc: Acc = Acc { key: "k" + "z", m: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = step(i, acc); i = i + 1; }
    return acc.m + acc.key.len();
}`, true},
	{"tuple-field-from-own-param", `struct Acc { t: (i32[], i32), m: i32 }
function step(n: i32, own st: Acc): Acc {
    let t: (i32[], i32) = st.t;
    let i: i32 = 0;
    while (i < n) { i = i + t.1; }
    return Acc { t: t, m: st.m + i };
}
function main(): i32 {
    let acc: Acc = Acc { t: ([1, 2], 1), m: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = step(i, acc); i = i + 1; }
    return acc.m + acc.t.0.len();
}`, true},
	{"tuple-element-from-own-param", `struct Frame { key: string, n: i32 }
struct Acc { fr: Frame, m: i32 }
function step(n: i32, own st: Acc): (Frame, i32) {
    let fr: Frame = st.fr;
    return (fr, st.m + n);
}
function main(): i32 {
    let acc: Acc = Acc { fr: Frame { key: "k" + "", n: 1 }, m: 0 };
    let i: i32 = 0;
    while (i < 5) {
        let (f, m) = step(i, acc);
        acc = Acc { fr: f, m: m };
        i = i + 1;
    }
    return acc.m + acc.fr.key.len() + acc.fr.n;
}`, false},
	// A view replaced by a fresh value at the top level owns that value when
	// it moves, so the literal takes it without a retain (#10482's guard
	// retained every moved local no sweep releases, and leaked here).
	{"view-rebound-fresh", `struct Frame { key: string, n: i32 }
struct Acc { fr: Frame, m: i32 }
@noinline
function fresh(n: i32): Frame { return Frame { key: "f" + "", n: n }; }
function step(n: i32, own st: Acc): Acc {
    let fr: Frame = st.fr;
    let m: i32 = fr.n;
    fr = fresh(m + n);
    return Acc { fr: fr, m: st.m + n };
}
function main(): i32 {
    let acc: Acc = Acc { fr: Frame { key: "k" + "", n: 1 }, m: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = step(i, acc); i = i + 1; }
    return acc.m + acc.fr.key.len() + acc.fr.n;
}`, true},
	// Replaced on one branch only: the other still holds the view, so the
	// literal keeps its retain.
	{"view-rebound-in-branch", `struct Frame { key: string, n: i32 }
struct Acc { fr: Frame, m: i32 }
@noinline
function fresh(n: i32): Frame { return Frame { key: "f" + "", n: n }; }
function step(n: i32, own st: Acc): Acc {
    let fr: Frame = st.fr;
    if (n % 2 == 0) { fr = fresh(n); }
    return Acc { fr: fr, m: st.m + n };
}
function main(): i32 {
    let acc: Acc = Acc { fr: Frame { key: "k" + "", n: 1 }, m: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = step(i, acc); i = i + 1; }
    return acc.m + acc.fr.key.len() + acc.fr.n;
}`, false},
}

// TestSelfHostFieldReadMove compiles each row with the emit drivers the
// compiler's own build goes through (asm_ir_run for x86-64 and arm64,
// wasm_ir_run for wasm) with the leak census on, and checks the answer
// against the interpreter. x86-64 runs again under FERN_SANITIZE, which must
// report no use-after-free or over-release.
func TestSelfHostFieldReadMove(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	arm64gcc, qemu := arm64Tooling(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Fatal("wasmtime not on PATH")
	}
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern", "wasm_ir_run.fern")
	asmDriver := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "asm_driver")
	wasmDriver := buildSelfHostBin(t, x86gcc, dir, "wasm_ir_run.fern", "wasm_driver")
	env := func(v string) []string { return []string{"PATH=/usr/bin:/bin", v} }

	for _, tc := range fieldReadMoveCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			src := []byte(tc.src + "\n")
			check := func(leg, stderr string, code int) {
				if code != want {
					t.Errorf("%s: exited %d, want %d (interp oracle)\n%s", leg, code, want, stderr)
				}
				if tc.balanced && !strings.Contains(stderr, "live_bytes=0") {
					t.Errorf("%s: census not balanced\n%s", leg, stderr)
				}
			}

			asm := runCaptureEnv(t, x86runner, asmDriver, src, env("FERN_LEAKCHECK=1"))
			stderr, code := runCaptureStderrExit(t, x86runner, buildBin(t, x86gcc, dir, tc.name, string(asm)))
			check("x86-64", stderr, code)

			asm = runCaptureEnv(t, x86runner, asmDriver, src, env("FERN_LEAKCHECK=1"), "-target", "arm64-linux")
			cmd := runArm64Bin(qemu, buildBinArm64(t, arm64gcc, dir, tc.name+"-arm64", string(asm)))
			var eb bytes.Buffer
			cmd.Stderr = &eb
			_ = cmd.Run()
			check("arm64", eb.String(), cmd.ProcessState.ExitCode())

			wat := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(wat, runCaptureEnv(t, x86runner, wasmDriver, src, env("FERN_LEAKCHECK=1"), "-ir"), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, code = runWasmCensus(t, wat)
			check("wasm", stderr, code)

			asm = runCaptureEnv(t, x86runner, asmDriver, src, env("FERN_SANITIZE=1"))
			stderr, code = runCaptureStderrExit(t, x86runner, buildBin(t, x86gcc, dir, tc.name+"-san", string(asm)))
			if code != want || strings.Contains(stderr, "use-after-free") || strings.Contains(stderr, "over-release") {
				t.Errorf("sanitize: exited %d, want %d, with no use-after-free or over-release\n%s", code, want, stderr)
			}
		})
	}
}
