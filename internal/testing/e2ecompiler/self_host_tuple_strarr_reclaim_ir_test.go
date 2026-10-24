package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupleStrArrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"tuple-strarr-churn", `function main(): i32 {
    let pre: string = "ab";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let t: (i32, string[]) = (i, [pre + "x", pre + "yy"]); acc = (acc + t.0 + t.1[0].len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let t: (i32, string[]) = (j, [pre + "x", pre + "yy"]); acc = (acc + t.0 + t.1[0].len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"tuple-strarr-opt-payload", `function main(): i32 {
    let pre: string = "ab";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let o: Option[(i32, string[])] = Some((i, [pre + "x", pre + "yy"]));
        match (o) { Some(t) => { acc = (acc + t.0) % 251; }, None => {} }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) {
        let o: Option[(i32, string[])] = Some((j, [pre + "x", pre + "yy"]));
        match (o) { Some(t) => { acc = (acc + t.0) % 251; }, None => {} }
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"tuple-strarr-value", `function main(): i32 {
    let pre: string = "ab";
    let t: (i32, string[]) = (7, [pre + "x", pre + "yy"]);
    let v: i32 = t.0 + t.1[0].len() + t.1[1].len() + t.1.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return v;
}`, 16},
	{"tuple-strarr-alias-safe", `function main(): i32 {
    let pre: string = "ab";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let s1: string = pre + "wide";
        let t: (i32, string[]) = (i, [s1, s1]);
        acc = (acc + t.0 + s1.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"tuple-strarr-escape-safe", `function main(): i32 {
    let pre: string = "ab";
    let keep: string[] = ["z"];
    let i: i32 = 0;
    while (i < 50) {
        let t: (i32, string[]) = (i, [pre + "x", pre + "yy"]);
        keep = t.1;
        i = i + 1;
    }
    let v: i32 = keep[0].len() + keep[1].len();
    if (__rc_underflow_count() != 0) { return 99; }
    if (v != 7) { return 97; }
    return 0;
}`, 0},
}

func TestSelfHostTupleStrArrReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range tupleStrArrReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = string[] element positions leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
