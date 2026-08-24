package e2eselfhost

import (
	"os/exec"
	"testing"
)

// `slice_unchecked(s, a, b): str` (#5634, D9, slice 3) on the self-host IR
// path, plus its total std/string sibling `s.slice_snap(a, b)` — which is
// plain stdlib Fern, so it comes along for free once the builtin lowers.
// Same modload-driver harness shape as the __ascii_run suite: the driver
// loads the program's stdlib closure, so the `import "std/string"` resolves.
//
// strSliceUncheckedSelfHostProg is SELF-CHECKING: 42 means every step
// matched; a failure returns the step's distinct small code. The multibyte
// fixture is "héllo" built from explicit bytes so the é's [195, 169]
// encoding is pinned rather than trusted to source encoding.
const strSliceUncheckedSelfHostProg = `import "std/string";

// h(104) é(195,169) l l o — 6 bytes, 5 code points.
function mk(): string {
    var b: u8[] = [104 as u8, 195 as u8, 169 as u8, 108 as u8, 108 as u8, 111 as u8];
    return string_from_bytes_unchecked(b);
}

function main(): i32 {
    var s: string = mk();
    if (s.len() != 6) { return 1; }

    // Byte-honest: the cut lands mid-é and keeps the lead byte.
    var cut: str = slice_unchecked(s, 0, 2);
    if (cut.len() != 2) { return 2; }
    if (cut[0] != 104) { return 3; }
    if (cut[1] != 195) { return 4; }

    // Boundary index forms: full range, empty range, suffix.
    if (slice_unchecked(s, 0, 6) != s) { return 5; }
    if (slice_unchecked(s, 3, 3).len() != 0) { return 6; }
    if (slice_unchecked(s, 3, 6) != "llo") { return 7; }

    // slice_snap: snap inward, clamp, inverted/empty, ASCII identity.
    if (s.slice_snap(0, 2) != "h") { return 8; }
    if (s.slice_snap(2, 6) != "llo") { return 9; }
    if (s.slice_snap(1, 3) != slice_unchecked(s, 1, 3)) { return 10; }
    if (s.slice_snap(0 - 5, 99) != s) { return 11; }
    if (s.slice_snap(4, 2) != "") { return 12; }
    if (s.slice_snap(2, 2) != "") { return 13; }
    if ("hello".slice_snap(1, 3) != "el") { return 14; }

    // Owned-temp source in a loop: the concat result is a temporary the
    // view borrows, so the lowering must keep it alive per iteration.
    var a: string = "ab";
    var c: string = "cd";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var t: str = slice_unchecked(a + c, 0, 2);
        if (t != "ab") { return 15; }
        acc = acc + t.len();
        i = i + 1;
    }
    if (acc != 100) { return 16; }
    return 42;
}
`

// runStrSliceUncheckedSelfHost compiles the program with the self-host
// modload driver for the given register target and returns the exit code.
func runStrSliceUncheckedSelfHost(t *testing.T, target string) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	if target == "arm64-linux" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64-linux"}
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}

	progAsm, progDir := compileSourceModload(t, runner, driverBin, strSliceUncheckedSelfHostProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "str_slice_unchecked", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostStrSliceUncheckedX86_64(t *testing.T) {
	if got := runStrSliceUncheckedSelfHost(t, "x86-64-linux"); got != 42 {
		t.Errorf("slice_unchecked self-host x86-64 = %d, want 42 (see strSliceUncheckedSelfHostProg for what each code means)", got)
	}
}

func TestSelfHostStrSliceUncheckedArm64(t *testing.T) {
	if got := runStrSliceUncheckedSelfHost(t, "arm64-linux"); got != 42 {
		t.Errorf("slice_unchecked self-host arm64 = %d, want 42 (see strSliceUncheckedSelfHostProg for what each code means)", got)
	}
}
