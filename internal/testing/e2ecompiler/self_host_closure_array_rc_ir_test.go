package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var closureArrayRcCases = []struct {
	name string
	src  string
	want int
}{
	{"literal-loop-call", `function go(n: i32): i32 {
    let fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 3) { s = s + fns[i](); i = i + 1; }
    return s;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 1},
	{"factory-and-param-bind", `function mkfns(n: i32): (() => i32)[] { let a: (() => i32)[] = [() => n, () => n + 1]; return a; }
function consume(fns: (() => i32)[]): i32 { let d = fns[0]; return d(); }
function go(n: i32): i32 {
    let fns = mkfns(n);
    let s: i32 = consume(fns);
    let i: i32 = 0;
    while (i < fns.len()) { s = s + fns[i](); i = i + 1; }
    return s;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 1},
	{"loop-local-and-block-local", `function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let fns: (() => i32)[] = [() => i, () => i + 1];
        if (i % 2 == 0) { let gs: (() => i32)[] = [() => i * 2]; t = t + gs[0](); }
        t = (t + fns[0]() + fns[1]()) % 251;
        i = i + 1;
    }
    return t % 7;
}`, 0},
	{"element-bind-loop", `function go(n: i32): i32 {
    let fns: (() => i32)[] = [() => n, () => n + 1];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { let d = fns[i % 2]; s = s + d(); i = i + 1; }
    let e = fns[0];
    return s + e() + fns[1]();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 5},
	{"closure-local-elements", `function build(n: i32): (() => i32)[] {
    let c = () => n * 3;
    let out: (() => i32)[] = [c];
    out = out.append(() => n + 1);
    return out;
}
function go(n: i32): i32 {
    let c = () => n;
    let fns: (() => i32)[] = [c, () => n + 1];
    fns = fns.append(c);
    let s: i32 = 0;
    for f in fns { s = s + f(); }
    let d = fns[2];
    let hs = build(n);
    return s + d() + c() + hs[0]() + hs[1]();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 2},
	{"closure-local-first-element-foreach", `function go(n: i32): i32 {
    let c = () => n;
    let fns: (() => i32)[] = [c, () => n + 1];
    let d = fns[0];
    let e = fns[1];
    let s: i32 = 0;
    for f in fns { s = s + f(); }
    return s + d() + e() + c() + fns.len();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 1},
	{"annotated-empty-append", `function go(n: i32): i32 {
    let fns: (() => i32)[] = [() => n];
    let c = () => n + 1;
    fns = fns.append(c);
    return fns[0]() + fns[1]() + c();
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 3},
	{"empty-append-read-before-append", `function go(n: i32): i32 {
    let fns: (() => i32)[] = [];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        if (i > 0) { let g = fns[0]; s = s + g(); }
        fns = fns.append(() => n + i);
        i = i + 1;
    }
    return s;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = (t + go(i)) % 251; i = i + 1; } return t % 7; }`, 6},
}

const closureArrayRcRefusalsSrc = `struct H { f: () => i32 }
function pick(n: i32): () => i32 { let fns: (() => i32)[] = [() => n, () => n + 1]; return fns[1]; }
function alias(n: i32): i32 { let fns: (() => i32)[] = [() => n]; let g = fns; return g[0]() + fns[0](); }
function stash(n: i32): i32 {
    let fns: (() => i32)[] = [() => n, () => n * 2];
    let keep: (() => i32)[] = [];
    for f in fns { keep = keep.append(f); }
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < keep.len()) { s = s + keep[i](); i = i + 1; }
    return s;
}
function hold(n: i32): i32 { let fns: (() => i32)[] = [() => n + 3]; let h: H = H { f: fns[0] }; return h.f(); }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let p = pick(i); t = (t + p() + alias(i) + stash(i) + hold(i)) % 251; i = i + 1; }
    return t % 7;
}`

const closureArrayRcFixpointSrc = `function mkfns(n: i32): (() => i32)[] { let a: (() => i32)[] = [() => n, () => n + 1]; return a; }
function early(n: i32): i32 {
    let fns: (() => i32)[] = [];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        if (i > 0) { let g = fns[0]; s = s + g(); }
        fns = fns.append(() => n + i);
        i = i + 1;
    }
    return s;
}
function go(n: i32): i32 {
    let fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 3) { s = s + fns[i](); i = i + 1; }
    let d = fns[1];
    let more: (() => i32)[] = [];
    more = more.append(() => n + 5);
    for f in more { s = s + f(); }
    let hs = mkfns(n);
    return s + d() + hs[0]() + hs[1]() + more.len() + early(n);
}
function churn(m: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let w: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    if (w != x) { return 97; }
    return 0;
}`

func TestSelfHostClosureArrayRcIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	census := func(t *testing.T, name, src string) (int, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		bin := buildBin(t, gcc, dir, name+"-census", asm)
		stderr, code := hevRun(t, runner, bin)
		allocs, _, live := parseLeakcheck(t, name, stderr)
		if allocs == 0 {
			t.Fatalf("%s: allocated nothing — the probe is not exercising the path", name)
		}
		return code, live
	}
	sanitize := func(t *testing.T, name, src string) (int, string) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_SANITIZE=1"})
		bin := buildBin(t, gcc, dir, name+"-sanitize", asm)
		stderr, code := hevRun(t, runner, bin)
		finding := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "fern-sanitizer:") && !strings.Contains(line, "leak") {
				finding = line
			}
		}
		return code, finding
	}

	for _, tc := range closureArrayRcCases {
		t.Run(tc.name, func(t *testing.T) {
			code, live := census(t, tc.name, tc.src)
			if code != tc.want {
				t.Errorf("%s: exit %d, want %d (native's)", tc.name, code, tc.want)
			}
			if live != 0 {
				t.Errorf("%s: live_bytes=%d at exit, want 0 — the closure array's element boxes leaked", tc.name, live)
			}
			scode, finding := sanitize(t, tc.name, tc.src)
			if scode != tc.want || finding != "" {
				t.Errorf("%s under FERN_SANITIZE: exit %d (want %d), finding %q — an over-release the census cannot see", tc.name, scode, tc.want, finding)
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		code, live := census(t, "refusals", closureArrayRcRefusalsSrc)
		if code != 0 {
			t.Errorf("refusals: exit %d, want 0 (native's)", code)
		}
		if live != 0 {
			t.Errorf("refusals: live_bytes=%d at exit, want 0", live)
		}
		scode, finding := sanitize(t, "refusals", closureArrayRcRefusalsSrc)
		if scode != 0 || finding != "" {
			t.Errorf("refusals under FERN_SANITIZE: exit %d (want 0), finding %q — an escaping element box was freed", scode, finding)
		}
	})
	t.Run("fixpoint", func(t *testing.T) {
		asm := hevCompile(t, runner, driverBin, closureArrayRcFixpointSrc, []string{"PATH=/usr/bin:/bin"})
		bin := buildBin(t, gcc, dir, "fixpoint", asm)
		if _, code := hevRun(t, runner, bin); code != 0 {
			t.Errorf("fixpoint exited %d, want 0 (99 = over-release; 98 = the closure arrays leaked; 97 = value corrupted)", code)
		}
	})
}

func TestSelfHostClosureArrayRcWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping closure-array RC wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(closureArrayRcFixpointSrc))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "closure-array-rc.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != 0 {
		t.Errorf("closure-array RC wasm IR fixpoint = %d, want 0 (99 = over-release; 98 = the closure arrays leaked; 97 = value corrupted)", got)
	}
}

func TestSelfHostClosureArrayRcIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(closureArrayRcFixpointSrc), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "closure-array-rc-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("closure-array RC arm64 fixpoint exited %d, want 0 (99 = over-release; 98 = the closure arrays leaked; 97 = value corrupted)", code)
	}
}
