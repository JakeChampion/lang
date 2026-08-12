package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// heapMarkCases pin #6728: `__heap_mark` / `__heap_release_to` — the one-level
// arena checkpoint — had no lowering in any self-host backend, so a program
// native builds and runs was refused:
//
//	FERN_STRICT_IR: main (call to unknown symbol __heap_mark)
//
// The pair exists FOR the self-host. `internal/checker` says so where it
// registers them — "built for the self-host per-module emit, whose per-unit
// accumulation otherwise exhausts the arena" — so the compiler that motivated
// the feature was the one that could not compile a program using it.
//
// Both register backends now lower the pair INLINE, for the reason
// `heap_bump_bytes` is inline: they read and write the arena globals directly,
// so routing through a call would only add an ABI.
//
// # What the release actually restores
//
// The cursor, and BOTH freelist head tables from a .bss shadow. The heads are
// snapshotted rather than cleared because a block allocated AND freed inside the
// window leaves a head pointing above the mark — after the cursor rewinds, a
// later pop and a later bump would both hand out that same address. Restoring
// the pre-mark heads drops exactly those entries while keeping the older ones
// reusable. A pre-mark block freed during the window is forgotten, which is a
// bounded leak rather than corruption.
//
// mark == 0 means the mark was taken before the first allocation seeded the
// cursor, i.e. no checkpoint: release leaves the cursor alone rather than zeroing
// it and handing out the arena base.
//
// # Why the reclaim case allocates with __raw_alloc
//
// The obvious probe — a loop building arrays — measures nothing, because RC
// reclaims each array to the freelist and the bump never advances. That is the
// RC layer working, not the checkpoint. `__raw_alloc` bumps and nothing frees it,
// so the growth is real and the rewind has something to give back: 125 KiB grown,
// under 1 KiB residual after the release.
//
// wasm is deliberately still refused, and now refuses UP FRONT with E066 from the
// capability layer (#6705) naming the targets that do provide `arena`, rather
// than bailing late on an unknown symbol — gated by the capability layer's own
// tests rather than here, since these drivers are bare emitters that do not run
// the capability pass.
var heapMarkCases = []struct {
	name string
	src  string
	want int
}{
	// The issue's reproducer: mark, release, return. Native exits 4; the
	// self-host refused to build it at all.
	{"heap-mark-roundtrip", `function main(): i32 {
    var m: i64 = __heap_mark();
    __heap_release_to(m);
    return 4;
}`, 4},
	// RECLAIM: the release must actually rewind the cursor. 2000 raw blocks grow
	// the bump ~125 KiB; after the release the residual is under 1 KiB, and the
	// pre-mark allocation is still intact.
	{"heap-mark-reclaims", `function main(): i32 {
    var seed: i32[] = [1, 2, 3];
    var m: i64 = __heap_mark();
    var before: i64 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 2000) { var p: i32 = __raw_alloc(64); i = i + 1; }
    var mid: i64 = __heap_bump_bytes();
    __heap_release_to(m);
    var after: i64 = __heap_bump_bytes();
    if (((mid - before) / 1024) as i32 < 64) { return 97; }
    if (((after - before) / 1024) as i32 > 1) { return 98; }
    if (seed[0] != 1) { return 96; }
    return 0;
}`, 0},
	// The arena is reusable after a release: a second window bumps from the
	// rewound cursor rather than from where the first one ended.
	{"heap-mark-reuse-after-release", `function main(): i32 {
    var seed: i32[] = [1, 2, 3];
    var m: i64 = __heap_mark();
    var before: i64 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 1000) { var p: i32 = __raw_alloc(64); i = i + 1; }
    __heap_release_to(m);
    var m2: i64 = __heap_mark();
    var j: i32 = 0;
    while (j < 1000) { var q: i32 = __raw_alloc(64); j = j + 1; }
    __heap_release_to(m2);
    var after: i64 = __heap_bump_bytes();
    if (((after - before) / 1024) as i32 > 1) { return 98; }
    if (seed[0] != 1) { return 96; }
    return 0;
}`, 0},
	// A release with a ZERO mark is a no-op, not a cursor reset — otherwise a
	// stray release would hand out the arena base. The allocation after it must
	// still be sound and distinct from the pre-existing one.
	{"heap-mark-zero-release-noop", `function main(): i32 {
    var keep: i32[] = [7, 8, 9];
    __heap_release_to(0i64);
    var fresh: i32[] = [1, 2, 3];
    if (keep[0] != 7) { return 96; }
    if (fresh[0] != 1) { return 95; }
    if (keep[2] != 9) { return 94; }
    return 0;
}`, 0},
}

// TestSelfHostHeapMarkIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run).
func TestSelfHostHeapMarkIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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

// TestSelfHostHeapMarkIRArm64 is the arm64 leg. It matters more than a mirror
// here: `-target arm64-linux` assembles and links IN PROCESS, so this is the
// only path that puts the emitted checkpoint through the self-host's own
// assembler rather than GNU as.
func TestSelfHostHeapMarkIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range heapMarkCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (97 = the probe never grew the arena; 98 = the release did not rewind; 94-96 = a live allocation was corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
