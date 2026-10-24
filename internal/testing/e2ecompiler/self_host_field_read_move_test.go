package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var fieldReadMoveCases = []struct {
	name string
	src  string
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
}`},
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
}`},
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
}`},
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
}`},
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
}`},
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
}`},
	{"view-introduced-in-branch", `struct Frame { key: string, n: i32 }
struct Acc { fr: Frame, m: i32 }
function step(n: i32, own st: Acc): Acc {
    let fr: Frame = Frame { key: "z" + "", n: 0 };
    if (n % 2 == 0) { fr = st.fr; }
    return Acc { fr: fr, m: st.m + n };
}
function main(): i32 {
    let acc: Acc = Acc { fr: Frame { key: "k" + "", n: 1 }, m: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = step(i, acc); i = i + 1; }
    return acc.m + acc.fr.key.len() + acc.fr.n;
}`},
	{"string-elem-from-own-param-array-field", `struct Bag { keys: string[], n: i32 }
struct Acc { key: string, m: i32 }
function step(n: i32, own bag: Bag): Acc {
    let k: string = bag.keys[0];
    return Acc { key: k, m: bag.n + n };
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 8) {
        let a: Acc = step(1, Bag { keys: ["k" + "x", "y" + ""], n: i });
        total = total + a.key.len() + a.m;
        i = i + 1;
    }
    return total;
}`},
}

func TestSelfHostFieldReadMove(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	arm64gcc, qemu := arm64Tooling(t)
	e2eharness.Wasmtime(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern", "drivers/wasm_ir_run.fern")
	asmDriver := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "asm_driver")
	wasmDriver := buildSelfHostBin(t, x86gcc, dir, "drivers/wasm_ir_run.fern", "wasm_driver")
	env := func(v string) []string { return []string{"PATH=/usr/bin:/bin", v} }

	for _, tc := range fieldReadMoveCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			src := []byte(tc.src + "\n")
			check := func(leg, stderr string, code int) {
				if code != want {
					t.Errorf("%s: exited %d, want %d (interp oracle)\n%s", leg, code, want, stderr)
				}
				if !strings.Contains(stderr, "live_bytes=0") {
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
			if err := os.WriteFile(wat, runCaptureEnv(t, x86runner, wasmDriver, src, env("FERN_LEAKCHECK=1")), 0o644); err != nil {
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
