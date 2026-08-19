package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strPayloadInterlockCases pin a fresh STRING local handed to a union element of
// an rc-tuple — `var sv = …; var t = (i, Some(sv))`.
//
// #7168 released a bare-ident ARRAY payload there and refused a string. The
// refusal was right but the reason recorded for it was not: the release is not
// missing, it cannot reach zero alone. The construction retains the payload
// (rc 2) and the tuple SUPPRESSES the local's own sweep, so exactly one
// reference was ever spent. Forced at the call site, on x86-64:
//
//	neither half          2 __fern_str_free   32 B/round
//	the "STR:" credit     3                   32
//	the element release   3                   32
//	both                  4                    0
//
// So the two halves land together under one condition, which is what the ARRAY
// twin has always done: alloc + inc paid by the local's rebind/exit release AND
// the payload dec in the reclaim block.
//
// The credit half is the tuple-element twin of the #4354 closure interlock and
// sits beside it; the release half is gated on slot_is_reclaimable_str, so the
// two cannot drift apart.
var strPayloadInterlockCases = []struct {
	name string
	src  string
	want int
}{
	// The gate. 32 on the parent across all three backends; native flat. Reads
	// `sv` after the tuple, so it doubles as the aliasing control.
	{"str-payload-reclaimed", `function churn(n: i32): i32 {
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
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// ESCAPE control: the same local also assigned to an outer one. The interlock
	// must NOT fire — tuple_union_payload_sole_use is not the thing that refuses
	// here, the plain escape walk is, because `keep = sv` is a second use outside
	// the tuple. It must stay refused AND must not over-release. 73 on native.
	{"str-escapes-must-refuse", `function churn(n: i32): i32 {
    var keep: string = "";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = "v" + i.to_string();
        var t: (i32, Option[string]) = (i, Some(sv));
        keep = sv;
        acc = (acc + t.0 + keep.len()) % 91;
        i = i + 1;
    }
    return (acc + keep.len()) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 73},
	// CARRIED-OUT control: the arm binds the payload and carries it past every
	// reclaim point. The binding must stay readable now that the payload is
	// actually released.
	{"str-payload-carried-out", `function churn(n: i32): i32 {
    var keep: string = "zz";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = "v" + i.to_string();
        var t: (i32, Option[string]) = (i, Some(sv));
        match (t.1) { Some(v) => { keep = v; }, None => {} }
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return (acc + keep.len()) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 5},
	// The ARRAY twin must be untouched by the interlock.
	{"arr-payload-unchanged", `function churn(n: i32): i32 {
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
}

const strPayloadInterlockFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostStrPayloadInterlockIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strPayloadInterlockCases {
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
				t.Errorf(strPayloadInterlockFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrPayloadInterlockIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strPayloadInterlockCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(strPayloadInterlockFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrPayloadInterlockWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string payload interlock wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strPayloadInterlockCases {
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
				t.Errorf(strPayloadInterlockFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
