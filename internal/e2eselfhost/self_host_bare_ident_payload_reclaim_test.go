package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// bareIdentPayloadReclaimCases pin a union element whose payload is a BARE
// IDENT rather than a construction — `(i, Some(xs))`.
//
// #7159 released the payload of a freshly-constructed one and refused this,
// because the release has to be NAMED and that site had no annotation to tell a
// bare-ident array (which a flat dec releases) from a bare-ident string (which a
// flat dec would misread as a pointer on the two-word-string backends). The
// slot carries what the annotation did not: is_arr_slot answers for the array.
//
// That freeing it is balanced is measured, not argued. The construction
// alias-incs the payload, so the element's dec spends the box's own reference
// and the local is still readable afterwards — `arr-read-after` reads `xs`
// after the tuple, and `arr-carried-out` reads a binding taken out of the arm
// past every reclaim point, both matching native.
//
// A string ARRAY is is_arr too, and a flat dec there would free the buffer and
// strand every element box, so it is refused rather than released.
//
// Byte cases return measured bytes per round; the two gates measure 40 on the
// parent across all three backends, native flat.
var bareIdentPayloadReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"bare-ident-array-payload", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[] = [i, i + 2];
        var t: (i32, Option[i32[]]) = (i, Some(xs));
        var r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v[0]; }, None => {} }
        acc = (acc + r + xs[1]) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The same shape with the local DEAD after the tuple: its own sweep fires at
	// a different point, so it is a separate measurement rather than a variant.
	{"bare-ident-array-payload-dead", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[] = [i, i + 2];
        var t: (i32, Option[i32[]]) = (i, Some(xs));
        var r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v[0]; }, None => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// ALIASING control: the arm carries the payload out to a local read after the
	// loop, past every reclaim point, and decoy allocations would be handed its
	// block if the dec had really freed it. 72 on native too.
	{"bare-ident-array-carried-out", `function churn(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[] = [i, i + 2];
        var t: (i32, Option[i32[]]) = (i, Some(xs));
        match (t.1) { Some(v) => { keep = v; }, None => {} }
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    var d1: i32[] = [777, 888];
    var d2: i32[] = [999, 555];
    return (acc + keep[0] + keep[1] + d1[0] + d2[0]) % 97;
}
function main(): i32 {
    var w: i32 = churn(100);
    var x: i32 = churn(100);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 72},
	// REFUSAL control: a bare-ident STRING payload. It keeps its leak, so this
	// pins the value and __rc_underflow() rather than a byte count — what must
	// not happen is an over-release, which would show as 99.
	{"bare-ident-string-refused", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = "v" + i.to_string();
        var t: (i32, Option[string]) = (i, Some(sv));
        var r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v.len(); }, None => {} }
        acc = (acc + r + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 46},
}

const bareIdentPayloadFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostBareIdentPayloadReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range bareIdentPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf(bareIdentPayloadFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostBareIdentPayloadReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range bareIdentPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(bareIdentPayloadFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostBareIdentPayloadReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping bare-ident payload reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range bareIdentPayloadReclaimCases {
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
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf(bareIdentPayloadFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
