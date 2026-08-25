package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An appended struct element whose SOURCE outlives the push ---------------
//
// `ps = ps.append(p)` where `p` is still live afterwards used to cost the
// CONTAINER its element walk. The append-built ARRSTRUCT credit admitted a bare
// ident only at a site the construction-move analysis had taken over, so a
// source that is read again, rebound, or pushed more than once made the element
// neither a move nor a fresh literal — and the whole array then reclaimed
// nothing but its buffer.
//
// The pairing that replaces the move requirement is the one slices 5 and 7
// established. The append RETAINS at every site the credit stamps ("APRETAIN:",
// issued by the same pass that grants the walk, so the inc and the dec are the
// same set of sites); the container's per-element field walk and the source's
// own release both run under __fern_rc_is_unique; whichever owner reaches rc 1
// does the deep work and the other takes the box dec. That holds in either
// order. A MOVED element is excluded from the stamp — it hands its single
// reference to the buffer and its own release is elided with the retain, so an
// inc there would strand the element at rc 1 after the walk.
//
// The source keeps its reclaim credit too: a stamped self-append is no longer a
// counted-SINK use, which is what the struct escape gate was refusing it for.
// Unstamped sinks — an append into an uncredited container, `with`, an array or
// tuple element, a variant payload — still refuse.
//
// Every want below was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count(): a release that ran under a live claim
// fails the row on the dangling direction rather than the leaking one.

type arrstructLiveElemCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const alelMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
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
    var p: P = P { xs: [i, i + 1], n: i };
    var ps: P[] = [];
    ps = ps.append(p);
    return (ps.len() + p.xs.len() + p.n) % 101;
}
` + alelMain,
			want: 4, allocs: 400, frees: 400,
		},
		{
			// A struct PARAM element: the CALLER owns the box, so the retain is
			// what lets the callee's element walk see it as shared and take the
			// box dec instead of freeing buffers the caller still reads. Without
			// the retain this is exit 99, not a leak.
			name: "param_element",
			src: decl + `function take(p: P): i32 {
    var ps: P[] = [];
    ps = ps.append(p);
    return ps.len();
}
function round(i: i32): i32 {
    var p: P = P { xs: [i, i + 1], n: i };
    var t: i32 = take(p);
    return (t + p.xs.len() + p.n) % 101;
}
` + alelMain,
			want: 4, allocs: 400, frees: 400,
		},
		{
			// The SAME box pushed four times. The walk decs it once per element
			// and finds it shared every time; `p`'s own release is the one that
			// reaches rc 1 and walks the fields. A per-site stamp is what makes
			// this work — an inc derived from the element's own credit would fire
			// zero times against four decs. Base: 400 / 200.
			name: "repeated_push_same_box",
			src: decl + `function round(i: i32): i32 {
    var p: P = P { xs: [i, i + 1], n: i };
    var ps: P[] = [];
    var k: i32 = 0;
    while (k < 4) { ps = ps.append(p); k = k + 1; }
    return (ps.len() + p.xs[0] + p.xs[1] + p.n) % 101;
}
` + alelMain,
			want: 4, allocs: 400, frees: 400,
		},
		{
			// The MOVED element, which must NOT be stamped: a block-scoped local
			// built and pushed inside the loop hands its single reference over.
			// It was clean before and stays clean — an inc here leaves every
			// element at rc 1 after the walk, which is a leak of the whole
			// structure.
			name: "moved_element_unchanged",
			src: decl + `function round(i: i32): i32 {
    var keep: P[] = [];
    var k: i32 = 0;
    while (k < 4) {
        var p: P = P { xs: [k, k + i], n: k };
        keep = keep.append(p);
        k = k + 1;
    }
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < keep.len()) { acc = acc + keep[j].xs[0] + keep[j].n; j = j + 1; }
    return acc % 101;
}
` + alelMain,
			want: 36, allocs: 1000, frees: 1000,
		},
		{
			// The source is REBOUND after the push. The container reclaims its
			// element (base was 200 frees, now 500), but `p`'s superseded box and
			// its xs still strand: a reassigned struct local earns no reclaim
			// credit at all, so nothing releases the value it holds at the exit.
			// Native is 600/600 here. That gap is the struct-local reassign
			// reclaim, which this slice does not build.
			name: "rebound_source_partial",
			src: decl + `function round(i: i32): i32 {
    var p: P = P { xs: [i, i + 1], n: i };
    var ps: P[] = [];
    ps = ps.append(p);
    p = P { xs: [i + 2, i + 3], n: i + 1 };
    return (ps.len() + p.n) % 101;
}
` + alelMain,
			want: 5, allocs: 600, frees: 500,
		},
	}
}

// TestSelfHostArrStructLiveElemX86_64 is the leak-accounting leg.
func TestSelfHostArrStructLiveElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrstructLiveElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
