package e2eselfhost

import (
	"bytes"
	"fmt"
	"os/exec"
	"testing"
)

// Self-host RC: `print` and `Writer.write` leave nothing behind per call
// (#8410).
//
// Both used to cost one block a call whatever their argument, with a string
// LITERAL too, so neither was the copying-builtin credit:
//
//   - print lowered to `print_str(s + "\n")`, and the joined temp — a fresh box
//     whose size tracked the argument — was released by nobody. eprint next to
//     it writes the payload and then the newline byte and so allocates nothing;
//     print now does the same with a second print_str of a "\n" literal, which
//     is also native's __fern_puts shape.
//   - Writer.write's Option[IoError] answer is a fresh rc=1 box __fern_writer_write
//     built for this caller, and a match over the call consumed it without
//     releasing it: 40 bytes a write, so a cat-shaped loop leaked 40 bytes a
//     chunk. The match now gives the box back.
//
// The shape each leg pins is INDEPENDENCE FROM THE ROUND COUNT: ten times the
// calls must cost the same live bytes. A per-call leak is exactly what that
// catches, and unlike an absolute byte count it does not go stale when some
// other allocation joins the probe. The narrow / wide pairs additionally pin
// independence from the ARGUMENT, which is what separated print's scaling temp
// from write's constant box in the first place.

// printLoopSrc prints `payload` `rounds` times. The test counts what reached
// fd 1, so a lost newline is a failure rather than a silently smaller program.
func printLoopSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < %d) { print("%s"); i = i + 1; }
    return 0;
}`, rounds, payload)
}

// writeLoopSrc is the same loop through `w.write(...)`, whose Option[IoError]
// answer the match consumes.
func writeLoopSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var w: Writer = stdout();
    var i: i32 = 0;
    while (i < %d) {
        match (w.write("%s")) { Some(_) => { return 9; }, None => {} }
        i = i + 1;
    }
    return 0;
}`, rounds, payload)
}

// writeGuardedArmSrc keeps the box off the in-arm release: a guarded arm can
// still fall through to the next one, so the release waits for the join. The
// box must be freed exactly once there, not zero times and not twice.
func writeGuardedArmSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var w: Writer = stdout();
    var i: i32 = 0;
    while (i < %d) {
        match (w.write("%s")) { Some(_) when i < 0 => { return 9; }, _ => {} }
        i = i + 1;
    }
    return 0;
}`, rounds, payload)
}

// writeReturningArmSrc leaves the function from INSIDE the matched arm — the
// last round's `None` arm returns rather than falling out of the match. Only the
// in-arm release covers that box; a join-only release never runs on the path
// that returns, and this probe is what tells the two apart.
func writeReturningArmSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var w: Writer = stdout();
    var i: i32 = 0;
    while (i < 1000000) {
        match (w.write("%s")) {
            Some(_) => { return 9; },
            None => { i = i + 1; if (i >= %d) { return 0; } }
        }
    }
    return 0;
}`, payload, rounds)
}

// runCapturingBoth runs a built binary and returns its stdout, stderr and exit
// code. The print probes assert on what actually reached fd 1.
func runCapturingBoth(t *testing.T, runner []string, bin string) ([]byte, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	return outBuf.Bytes(), errBuf.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostPrintWriteBoxReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const (
		few  = 20
		many = 200
	)
	const narrow = "ab"
	const wide = "abcdefghabcdefghabcdefgh"

	for _, tc := range []struct {
		name string
		src  func(payload string, rounds int) string
		// newline: this probe's call adds one to what reaches fd 1.
		newline bool
	}{
		{name: "print", src: printLoopSrc, newline: true},
		{name: "writer_write", src: writeLoopSrc},
		{name: "writer_write_guarded_arm", src: writeGuardedArmSrc},
		{name: "writer_write_returning_arm", src: writeReturningArmSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := func(label, payload string, rounds int) (int64, int64, int64) {
				src := tc.src(payload, rounds)
				asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
				bin := buildBin(t, gcc, dir, tc.name+"_"+label, asm)
				stdout, stderr, exit := runCapturingBoth(t, runner, bin)
				if exit != 0 {
					t.Fatalf("%s/%s exited %d (9 = the write reported an error; 99 = rc underflow), stderr:\n%s",
						tc.name, label, exit, stderr)
				}
				per := len(payload)
				if tc.newline {
					per++
				}
				if want := per * rounds; len(stdout) != want {
					t.Errorf("%s/%s wrote %d bytes on fd 1, want %d — the payload or its newline is wrong",
						tc.name, label, len(stdout), want)
				}
				return leakSummaryOf(t, tc.name+"/"+label, stderr)
			}
			fa, ff, fl := measure("few", narrow, few)
			ma, mf, ml := measure("many", narrow, many)
			wa, wf, wl := measure("wide", wide, many)
			// Ten times the calls at the same argument: a per-call leak is
			// the only thing that can move the live bytes here.
			if fl != ml {
				t.Errorf("live_bytes %d rounds=%d vs %d rounds=%d (allocs %d/%d, frees %d/%d): "+
					"the call leaves a block behind every round",
					fl, few, ml, many, fa, ma, ff, mf)
			}
			// …and 12x the argument at the same call count, which is what
			// told print's scaling temp from write's constant box.
			if ml != wl {
				t.Errorf("live_bytes narrow=%d wide=%d (allocs %d/%d, frees %d/%d): "+
					"the call's own block scales with its argument", ml, wl, ma, wa, mf, wf)
			}
			if ma != mf || ml != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — every block the loop allocates must be freed",
					tc.name, ma, mf, ml)
			}
		})
	}
}
