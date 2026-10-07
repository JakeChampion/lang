package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An appended struct element whose SOURCE outlives the push ---------------
//
// `ps = ps.append(p)` where `p` is still live afterwards: the array and `p`
// both own the element box. Whichever owner releases last frees the element's
// `xs` buffer and its box; the other only drops its count. That has to hold in
// either order and for every shape below: a read after the push, a struct
// parameter the caller owns, one box pushed several times, and the source
// rebound while the array still holds the old box. A MOVED element (the push
// is the source's last use) hands its single reference to the array and takes
// no retain.
//
// Each row pins the exit value, and the leak-accounting leg pins allocs and
// frees. main returns 99 when __rc_underflow_count() is non-zero, so a release
// that runs while another owner is live fails on the dangling direction rather
// than the leaking one.

type arrstructLiveElemCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const alelMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func arrstructLiveElemCases() []arrstructLiveElemCase {
	const decl = "struct P { xs: i32[], n: i32 }\n"
	return []arrstructLiveElemCase{
		{
			// THE REPRO. The push is not `p`'s last use, so nothing is moved and
			// both owners are live at the sweep. Base: 400 allocs / 200 frees —
			// only the two buffers came back, every element box and its xs
			// stranded.
			name: "read_after_push",
			src: decl + `function round(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], n: i };
    let ps: P[] = [];
    ps = ps.append(p);
    return (ps.len() + p.xs.len() + p.n) % 101;
}
` + alelMain,
			want: 4, allocs: 300, frees: 300,
		},
		{
			// A struct PARAM element: the CALLER owns the box, so the retain is
			// what lets the callee's element walk see it as shared and take the
			// box dec instead of freeing buffers the caller still reads. Without
			// the retain this is exit 99, not a leak.
			name: "param_element",
			src: decl + `function take(p: P): i32 {
    let ps: P[] = [];
    ps = ps.append(p);
    return ps.len();
}
function round(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], n: i };
    let t: i32 = take(p);
    return (t + p.xs.len() + p.n) % 101;
}
` + alelMain,
			want: 4, allocs: 300, frees: 300,
		},
		{
			// The SAME box pushed four times. The walk decs it once per element
			// and finds it shared every time; `p`'s own release is the one that
			// reaches rc 1 and walks the fields. A per-site stamp is what makes
			// this work — an inc derived from the element's own credit would fire
			// zero times against four decs. Base: 400 / 200.
			name: "repeated_push_same_box",
			src: decl + `function round(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], n: i };
    let ps: P[] = [];
    let k: i32 = 0;
    while (k < 4) { ps = ps.append(p); k = k + 1; }
    return (ps.len() + p.xs[0] + p.xs[1] + p.n) % 101;
}
` + alelMain,
			want: 4, allocs: 300, frees: 300,
		},
		{
			// The MOVED element, which must NOT be stamped: a block-scoped local
			// built and pushed inside the loop hands its single reference over.
			// It was clean before and stays clean — an inc here leaves every
			// element at rc 1 after the walk, which is a leak of the whole
			// structure.
			name: "moved_element_unchanged",
			src: decl + `function round(i: i32): i32 {
    let keep: P[] = [];
    let k: i32 = 0;
    while (k < 4) {
        let p: P = P { xs: [k, k + i], n: k };
        keep = keep.append(p);
        k = k + 1;
    }
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < keep.len()) { acc = acc + keep[j].xs[0] + keep[j].n; j = j + 1; }
    return acc % 101;
}
` + alelMain,
			want: 36, allocs: 900, frees: 900,
		},
		{
			// The source is REBOUND after the push. The rebind's field reclaim
			// finds the old box shared and hands back this slot's count instead
			// of freeing anything, so the container's walk reaches rc 1 and does
			// the deep work. Base: 600 allocs / 200 frees.
			name: "rebound_source",
			src: decl + `function round(i: i32): i32 {
    let p: P = P { xs: [i, i + 1], n: i };
    let ps: P[] = [];
    ps = ps.append(p);
    p = P { xs: [i + 2, i + 3], n: i + 1 };
    return (ps.len() + p.n) % 101;
}
` + alelMain,
			want: 5, allocs: 500, frees: 500,
		},
		{
			// A rebind while the container still holds the old box, read
			// back as a value: freeing the old xs early reads garbage where
			// native reads 15 per round. The junk arrays are not heap-
			// allocated on the typed lowering, so the count covers only the
			// P values.
			name: "rebound_source_element_still_readable",
			src: decl + `function round(i: i32): i32 {
    let p: P = P { xs: [7, 8], n: i };
    let ps: P[] = [];
    ps = ps.append(p);
    p = P { xs: [111, 222], n: i + 1 };
    let junk: i32[] = [333, 444];
    let junk2: i32[] = [555, 666];
    return ps[0].xs[0] + ps[0].xs[1] + junk[0] - junk[0] + junk2[0] - junk2[0];
}
` + alelMain,
			want: 45, allocs: 300, frees: 300,
		},
	}
}

// TestSelfHostArrStructLiveElemX86_64 is the leak-accounting leg.
func TestSelfHostArrStructLiveElemX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrstructLiveElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructlive_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a release ran "+
					"under a live claim)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER means a release credit "+
					"stopped resolving; MORE means the stamp reached a site the "+
					"append does not retain", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostArrStructLiveElemWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostArrStructLiveElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arrstruct live-element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrstructLiveElemCases() {
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
			watFile := filepath.Join(dir, "arrstructlive_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("arrstruct live-element wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostArrStructLiveElemIRArm64 — the arm64 sibling under qemu.
func TestSelfHostArrStructLiveElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrstructLiveElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "arrstructlive_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
