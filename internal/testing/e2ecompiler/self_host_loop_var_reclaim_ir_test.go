package e2ecompiler

import (
	"os/exec"
	"testing"
)

var loopVarReclaimCases = []struct {
	name string
	decl string
	body string
	want int
}{
	{
		name: "array",
		body: `        let row: i32[] = [i, i * 2, i * 3];
        acc = acc + (row[0] + row[1] + row[2]) - (i * 6);`,
		want: 7,
	},
	{
		name: "struct",
		decl: `struct Pt { x: i32, y: i32 }`,
		body: `        let p: Pt = Pt { x: i, y: i + 1 };
        acc = acc + (p.y - p.x) - 1;`,
		want: 7,
	},
	{
		name: "enum-payload-reclaimed",
		decl: `enum Box { Val(i32[]), Empty }
function head(b: Box): i32 { match (b) { Val(xs) => { return xs[0]; }, Empty => { return 0; } } }`,
		body: `        let b: Box = Val([i, i + 7]);
        acc = acc + head(b) - i;`,
		want: 7,
	},
	{
		name: "string-concat-temps-reclaimed",
		decl: `function suffix(n: i32): string { if (n % 2 == 0) { return "even"; } return "odd"; }`,
		body: `        let s: string = "v-" + suffix(i);
        acc = acc + s.len() - 2 - suffix(i).len();`,
		want: 7,
	},
}

func loopVarSrc(decl, body string) string {
	return decl + `
function work(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
` + body + `
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = work(100);
    let b1: i64 = __heap_bump_bytes();
    let m: i32 = work(100);
    let b2: i64 = __heap_bump_bytes();
    if (w != 0) { return 1; }
    if (m != 0) { return 2; }
    if ((b2 - b1) == (0 as i64)) { return 7; }
    return 3;
}
`
}

func TestSelfHostLoopVarReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range loopVarReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := loopVarSrc(tc.decl, tc.body)
			asm := runCapture(t, gcc, runner, driverBin, []byte(src))
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
				t.Errorf("%s = %d, want %d (7 = reclaimed; 3 = the bump mark grew across an identical second run; 1/2 = the read-back drifted, i.e. a live buffer was handed out)",
					tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostLoopVarReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range loopVarReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := loopVarSrc(tc.decl, tc.body)
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (7 = reclaimed; 3 = the bump mark grew; 1/2 = the read-back drifted)",
					tc.name, code, tc.want)
			}
		})
	}
}
