package e2ecompiler

import (
	"bytes"
	"fmt"
	"os/exec"
	"testing"
)

func printLoopSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let i: i32 = 0;
    while (i < %d) { print("%s"); i = i + 1; }
    return 0;
}`, rounds, payload)
}

func writeLoopSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let w: Writer = stdout();
    let i: i32 = 0;
    while (i < %d) {
        match (w.write("%s")) { Err(_) => { return 9; }, Ok(_) => {} }
        i = i + 1;
    }
    return 0;
}`, rounds, payload)
}

func writeGuardedArmSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let w: Writer = stdout();
    let i: i32 = 0;
    while (i < %d) {
        match (w.write("%s")) { Err(_) when i < 0 => { return 9; }, _ => {} }
        i = i + 1;
    }
    return 0;
}`, rounds, payload)
}

func writeReturningArmSrc(payload string, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let w: Writer = stdout();
    let i: i32 = 0;
    while (i < 1000000) {
        match (w.write("%s")) {
            Err(_) => { return 9; },
            Ok(_) => { i = i + 1; if (i >= %d) { return 0; } }
        }
    }
    return 0;
}`, payload, rounds)
}

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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const (
		few  = 20
		many = 200
	)
	const narrow = "ab"
	const wide = "abcdefghabcdefghabcdefgh"

	for _, tc := range []struct {
		name    string
		src     func(payload string, rounds int) string
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
			if fl != ml {
				t.Errorf("live_bytes %d rounds=%d vs %d rounds=%d (allocs %d/%d, frees %d/%d): "+
					"the call leaves a block behind every round",
					fl, few, ml, many, fa, ma, ff, mf)
			}
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
