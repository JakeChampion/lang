package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// consumedAppendCases pin #6501's self-host half: an `.append` result handed
// straight to a borrowing call — `sink(path.append(i))` — is owned by nobody
// once the callee has read it, so one whole array buffer leaked per call. That
// is how an immutable trail is threaded down a recursion (backtracking, DFS
// with a path, subset and permutation enumeration), so the leak was unbounded
// on the ordinary path.
//
// One post-call release covers BOTH of __fern_arr_push_grow's outcomes, which
// is why no is_unique test is needed: the copy path hands back a fresh buffer
// at rc 1, which the dec frees, and the in-place path bumps the receiver to
// rc 2 before handing it back, which the dec nets down to the receiver's own
// single reference. Reclaiming one the way the other wants is a use-after-free,
// so every case here reads its receiver again afterwards — a release of the
// wrong buffer is a wrong ANSWER, not merely a byte count.
//
// SCALAR elements only. The grow's copy path memcpys elements without
// retaining them, so a pointer-element copy aliases the original's elements
// and the deep release such a buffer earns would free them out from under it.
// pointer-elem-receiver-is-refused pins that exclusion.
var consumedAppendCases = []struct {
	name string
	src  string
	want int
	wasm bool
}{
	// The grow COPIES: `full` has no spare capacity, so the temp is a fresh
	// buffer nobody else holds and the dec frees it. 56 B/round before.
	{"copy-path-receiver-read-after", `function sink(xs: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function work(n: i32): i32 {
    var full: i32[] = [n, n + 1];
    var a: i32 = sink(full.append(10));
    return a + sink(full) + full.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + work(i % 8)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = (acc + work(j % 8)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// The grow mutates IN PLACE: `roomy` has spare capacity at rc==1, so the
	// helper bumps the receiver's count and hands the RECEIVER back. The same
	// dec must net that bump away and free nothing — `roomy` is summed again
	// after the call to say so.
	{"inplace-path-frees-nothing", `function sink(xs: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function work(n: i32): i32 {
    var roomy: i32[] = [];
    var i: i32 = 0;
    while (i < 3) { roomy = roomy.append(n + i); i = i + 1; }
    var a: i32 = sink(roomy.append(20));
    var b: i32 = sink(roomy);
    if (a <= b) { return 0 - 1; }
    return a + b + roomy.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var v: i32 = work(i % 8); if (v < 0) { return 97; } acc = (acc + v) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { var w: i32 = work(j % 8); if (w < 0) { return 97; } acc = (acc + w) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, 0, true},

	// The ELEMENT-receiver spelling: the container still owns what the append
	// read, and `xs[0]` is not an Ident so it reaches the classifier by a
	// different route than a bare local.
	{"element-receiver", `function sink(xs: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function work(n: i32): i32 {
    var xs: i32[][] = [[n], [n + 1]];
    var a: i32 = sink(xs[0].append(30));
    return a + sink(xs[0]) + sink(xs[1]) + xs[0].len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + work(i % 8)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = (acc + work(j % 8)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// The shape the leak class is named for: a PARAM receiver appended once
	// per iteration and consumed immediately. 8 leaks per call before, so this
	// is the case that was unbounded in the way a real backtracking search is.
	{"param-receiver-in-loop", `function sink(xs: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function walk(path: i32[]): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 8) { t = t + sink(path.append(i)); i = i + 1; } return t; }
function work(n: i32): i32 {
    var full: i32[] = [n, n + 1];
    return walk(full) + sink(full) + full.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + work(i % 8)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = (acc + work(j % 8)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// REFUSED, and it must stay refused: a string[] receiver's copy path
	// memcpys element POINTERS without retaining them, so releasing the temp
	// through the deep walk such a buffer earns would free strings the
	// original still holds. Values must stay correct — this one still leaks,
	// so it is deliberately not asserted flat.
	{"pointer-elem-receiver-is-refused", `function sink(xs: string[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i].len(); i = i + 1; } return s; }
function tag(k: i32): string { if (k == 0) { return "zero"; } return "many"; }
function work(n: i32): i32 {
    var ss: string[] = ["alpha-" + tag(n)];
    var a: i32 = sink(ss.append("beta-" + tag(n)));
    var b: i32 = sink(ss);
    if (a <= b) { return 0 - 1; }
    return a + b + ss.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) { var v: i32 = work(i % 2); if (v < 0) { return 97; } acc = (acc + v) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},
}

// TestSelfHostConsumedAppendReclaimX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostConsumedAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range consumedAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = append temps leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostConsumedAppendReclaimArm64 is the arm64 leg.
func TestSelfHostConsumedAppendReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range consumedAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = append temps leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostConsumedAppendReclaimWasmIR is the wasm leg.
func TestSelfHostConsumedAppendReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping consumed-append reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range consumedAppendCases {
		if !tc.wasm {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (98 = append temps leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
