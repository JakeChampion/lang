package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var heapMarkCases = []struct {
	name string
	src  string
	want int
}{
	{"heap-mark-roundtrip", `function main(): i32 {
    let m: i64 = __heap_mark();
    __heap_release_to(m);
    return 4;
}`, 4},
	{"heap-mark-reclaims", `function main(): i32 {
    let seed: i32[] = [(__heap_bump_bytes() as i32) + 1, 2, 3];
    let m: i64 = __heap_mark();
    let before: i64 = __heap_bump_bytes();
    let i: i32 = 0;
    while (i < 2000) { let p: usize = __raw_alloc(64); i = i + 1; }
    let mid: i64 = __heap_bump_bytes();
    __heap_release_to(m);
    let after: i64 = __heap_bump_bytes();
    if (((mid - before) / 1024) as i32 < 64) { return 97; }
    if (((after - before) / 1024) as i32 > 1) { return 98; }
    if (seed[0] != 1) { return 96; }
    return 0;
}`, 0},
	{"heap-mark-reuse-after-release", `function main(): i32 {
    let seed: i32[] = [(__heap_bump_bytes() as i32) + 1, 2, 3];
    let m: i64 = __heap_mark();
    let before: i64 = __heap_bump_bytes();
    let i: i32 = 0;
    while (i < 1000) { let p: usize = __raw_alloc(64); i = i + 1; }
    __heap_release_to(m);
    let m2: i64 = __heap_mark();
    let j: i32 = 0;
    while (j < 1000) { let q: usize = __raw_alloc(64); j = j + 1; }
    __heap_release_to(m2);
    let after: i64 = __heap_bump_bytes();
    if (((after - before) / 1024) as i32 > 1) { return 98; }
    if (seed[0] != 1) { return 96; }
    return 0;
}`, 0},
	{"heap-mark-zero-release-noop", `function main(): i32 {
    let keep: i32[] = [(__heap_bump_bytes() as i32) + 7, 8, 9];
    __heap_release_to(0i64);
    let fresh: i32[] = [keep[0] - 6, 2, 3];
    if (keep[0] != 7) { return 96; }
    if (fresh[0] != 1) { return 95; }
    if (keep[2] != 9) { return 94; }
    return 0;
}`, 0},
}

func TestSelfHostHeapMarkIRX86_64(t *testing.T) {
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

	for _, tc := range heapMarkCases {
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
				t.Errorf("%s = %d, want %d (97 = the probe never grew the arena; 98 = the release did not rewind; 94-96 = a live allocation was corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostHeapMarkIRArm64(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, tc := range heapMarkCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (97 = the probe never grew the arena; 98 = the release did not rewind; 94-96 = a live allocation was corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
