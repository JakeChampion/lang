package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A struct-literal `string[]` field costs the SOURCE its element walk -----
//
// `let src: string[] = mkv(i); if (..) { let p: P = P { f: src, n: i }; .. }`
// must free every element box whether or not the holder is built: the source's
// own release walks its elements, rather than leaving the walk to a holder that
// may never exist. Skipping the holder on half the rounds is the repro.
//
// __fern_str_arr_free is rc-gated — rc>1 decs and leaves the elements to the
// other owner, and only the LAST owner at rc 1 walks — so the walk cannot run
// twice. Exit 99 is reserved for __rc_underflow_count().
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. A pre-fusion number quoted in a row note below is
// twice its pin.

type strArrFieldSourceCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const safsPrelude = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
`

const safsMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func strArrFieldSourceCases() []strArrFieldSourceCase {
	return []strArrFieldSourceCase{
		{
			// THE REPRO: the holder is built on half the rounds. Base 650/450.
			name: "conditional_holder",
			src: safsPrelude + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    return (t + i) % 101;
}
` + safsMain,
			want: 63, allocs: 350, frees: 350,
		},
		{
			// The same, with the source READ after the conditional — which for
			// `string` was the whole discriminator and here changes nothing.
			// Base 650/450, identical to the row above.
			name: "conditional_holder_source_read_after",
			src: safsPrelude + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    return (t + src.len() + src[0].len() + i) % 101;
}
` + safsMain,
			want: 30, allocs: 350, frees: 350,
		},
		{
			// THE ROW THAT CARRIES THE SOUNDNESS. The danger here is not a leak
			// but a DOUBLE element walk — the holder's field drop and the
			// source's sweep both walking. So this reads the source's ELEMENTS
			// back after the holder has died, with two fresh string arrays
			// allocated in between: a box freed twice is reused before the read
			// and the answer stops matching native's. Base 1850/1650.
			name: "elements_read_back_after_churn",
			src: safsPrelude + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    let c1: string[] = mkv(i + 7);
    let c2: string[] = mkv(i + 9);
    return (t + src.len() + src[0].len() + src[1].len() + c1[0].len() - c1[0].len() + c2[1].len() - c2[1].len() + i) % 101;
}
` + safsMain,
			want: 96, allocs: 950, frees: 950,
		},
		{
			// CONTROL, and the row that says the move axis is NOT the
			// discriminator for this type: the unconditional store was already
			// clean at 700/700 before this change. If it ever exceeds 700 the
			// forgiveness has reached a store whose retain was elided.
			name: "unconditional_holder_unchanged",
			src: safsPrelude + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}
` + safsMain,
			want: 71, allocs: 400, frees: 400,
		},
		{
			// CONTROL: an `if` whose condition happens to always hold. Same
			// statement kind as the repro, same emitted code, and already clean
			// — which is what rules out "it is the StmtIf arm" as the cause.
			name: "always_entered_if_unchanged",
			src: safsPrelude + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let t: i32 = 0;
    if (i >= 0) { let p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    return (t + src.len() + i) % 101;
}
` + safsMain,
			want: 70, allocs: 400, frees: 400,
		},
		{
			// The holder ESCAPES by return, so it earns no struct credit and the
			// source is refused. Already clean at 700/700 — pinned so that a
			// forgiveness which ignored the credited-holder condition would show
			// up as frees running ABOVE 700 rather than passing unnoticed.
			name: "escaping_holder_unchanged",
			src: safsPrelude + `function mk(i: i32): P {
    let src: string[] = mkv(i);
    let p: P = P { f: src, n: i };
    if (src.len() > 1) { return p; }
    return p;
}
function round(i: i32): i32 {
    let p: P = mk(i);
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}
` + safsMain,
			want: 71, allocs: 400, frees: 400,
		},
	}
}

// TestSelfHostStrArrFieldSourceX86_64 is the leak-accounting leg.
func TestSelfHostStrArrFieldSourceX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "safs_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: both owners walked "+
					"the elements instead of only the one at rc 1)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER on a conditional row means the "+
					"source lost its DEEP \"SARR:\" credit again; MORE on any control "+
					"means the forgiveness reached a store whose retain was elided or "+
					"whose holder runs no field drop", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStrArrFieldSourceWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostStrArrFieldSourceWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping strarr field-source wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strArrFieldSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "safs_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("strarr field-source wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldSourceIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrArrFieldSourceIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "safs_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
