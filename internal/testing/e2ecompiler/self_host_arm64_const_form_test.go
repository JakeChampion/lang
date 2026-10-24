package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func arm64ShapeHarness(t *testing.T) (func(t *testing.T, src string) string, func(t *testing.T, name, asm string) int) {
	t.Helper()
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-target", "arm64-linux")
		} else {
			args := append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64-linux")
			cmd = exec.Command(x86runner[0], args...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}

	run := func(t *testing.T, name, asm string) int {
		t.Helper()
		asmPath := filepath.Join(dir, name+".s")
		binPath := filepath.Join(dir, name)
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(arm64gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		inner := runArm64Bin(qemu, binPath)
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", name)
		}
		return inner.ProcessState.ExitCode()
	}
	return emit, run
}

func runArm64ShapeCases(t *testing.T, cases []shapeCase) {
	t.Helper()
	emit, run := arm64ShapeHarness(t)
	runShapeCases(t, emit, run, cases)
}

func TestSelfHostConstOperandReachesImmediateFormArm64(t *testing.T) {
	runArm64ShapeCases(t, []shapeCase{
		{
			name: "alu",
			src: `@noinline function bump(x: i64): i64 { return x + 1i64; }
@noinline function down(x: i32): i32 { return x - 4095; }
@noinline function less(x: i32): boolean { return x < 7; }
function main(): i32 {
    let i: i64 = 0i64; let s: i64 = 0i64;
    while (i < 3i64) { s = s + bump(i); if (less(i as i32)) { s = s + 100i64; } i = i + 1i64; }
    return ((s % 100i64) as i32) + down(4100);
}`,
			want: (300+1+2+3)%100 + 5,
			has: map[string][]string{
				"bump": {`\n    add (x[0-9]+), \1, #1\n`},
				"down": {`\n    sub (x[0-9]+), \1, #4095\n`},
				"less": {`\n    cmp x[0-9]+, #7\n    cset x[0-9]+, lt\n`},
			},
			lacks: map[string][]string{
				"bump": {`mov x[0-9]+, #1\n`, `add x[0-9]+, x[0-9]+, x[0-9]+`, `str x[0-9]+, \[sp, #-16\]!`},
				"down": {`#4095\n    sub`, `sub x[0-9]+, x[0-9]+, x[0-9]+`},
				"less": {`mov x[0-9]+, #7\n`, `cmp x[0-9]+, x[0-9]+`},
			},
		},
		{
			name: "refused-widths",
			src: `@noinline function page(x: i32): i32 { return x + 4096; }
@noinline function odd(x: i32): i32 { return x + 4097; }
@noinline function wide(x: i64): i64 { return x + 70000i64; }
function main(): i32 { return page(1) - 4000 + odd(1) - 4090 + ((wide(1i64) % 100i64) as i32); }`,
			want: 97 + 8 + 1,
			has: map[string][]string{
				"page": {`\n    mov (x[0-9]+), #4096\n    add (x[0-9]+), \2, \1\n`},
				"odd":  {`\n    mov (x[0-9]+), #4097\n    add (x[0-9]+), \2, \1\n`},
				"wide": {`\n    ldr (x[0-9]+), =70000\n    add (x[0-9]+), \2, \1\n`},
			},
			lacks: map[string][]string{
				"page": {`add x[0-9]+, x[0-9]+, #4096`, `str x[0-9]+, \[sp, #-16\]!`},
				"odd":  {`add x[0-9]+, x[0-9]+, #4097`, `str x[0-9]+, \[sp, #-16\]!`},
				"wide": {`add x[0-9]+, x[0-9]+, #70000`, `mov x[0-9]+, #70000`, `str x[0-9]+, \[sp, #-16\]!`},
			},
		},
	})
}

func TestSelfHostI64ConstantTakesMovzFormArm64(t *testing.T) {
	runArm64ShapeCases(t, []shapeCase{
		{
			name: "forms",
			src: `@noinline function small(): i64 { return 65535i64; }
@noinline function shifted(): i64 { return 65536i64; }
@noinline function wide(): i64 { return 65537i64; }
@noinline function neg(): i64 { return 0i64 - 5i64; }
function main(): i32 { return ((small() + shifted() + wide() + neg()) % 250i64) as i32; }`,
			want: (65535 + 65536 + 65537 - 5) % 250,
			has: map[string][]string{
				"small":   {`\n    mov x[0-9]+, #65535\n`},
				"shifted": {`\n    movz x[0-9]+, #1, lsl #16\n`},
				"wide":    {`\n    ldr x[0-9]+, =65537\n`},
				"neg":     {`\n    mov x[0-9]+, #-5\n`},
			},
			lacks: map[string][]string{
				"small":   {`ldr x[0-9]+, =`},
				"shifted": {`ldr x[0-9]+, =`},
				"wide":    {`mov x[0-9]+, #65537`},
				"neg":     {`ldr x[0-9]+, =`, `sub x[0-9]+`},
			},
		},
	})
}
