package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostCLIStage2X86_64 gates the CLI reproducing itself on x86-64
// (#7954). Stage 1 is fern.fern built by the Go backend; stage 2 is fern.fern
// built by stage 1. Stage 2 then compiles lexer.fern and a small program, and
// the program must run and answer correctly.
//
// Nothing else runs a compiler the self-host built over real input: the
// fixpoints compare emitted bytes, and this failed (arena exhaustion on
// parser.fern, SIGSEGV on checker.fern and fern.fern) while every one of them
// was green. Stage 2 rebuilding the whole compiler is FERN_STAGE3=1, since it
// doubles the cost.
func TestSelfHostCLIStage2X86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the stage-2 compiler takes host paths as argv; native x86-64 only")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	stage1 := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	compile := func(t *testing.T, compiler, src, out string) {
		t.Helper()
		cmd := exec.Command(compiler, "-target", "x86-64-linux", "-o", out, src, stdlibRoot)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed on %s: %v\n%s", filepath.Base(compiler), filepath.Base(src), err, b)
		}
	}

	stage2 := filepath.Join(dir, "fern_stage2")
	compile(t, stage1, filepath.Join(dir, "fern.fern"), stage2)

	t.Run("lexer", func(t *testing.T) {
		compile(t, stage2, langSrcAbs(t, "examples/self_host/lexer.fern"), filepath.Join(dir, "lexer_stage2"))
	})

	t.Run("program", func(t *testing.T) {
		src := filepath.Join(dir, "stage2_prog.fern")
		prog := `struct P { name: string, xs: i32[] }
function total(p: P): i32 { let s: i32 = 0; for x in p.xs { s = s + x; } return s + p.name.len(); }
function main(): i32 {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 5) { ps = ps.append(P { name: "p" + "q", xs: [i, i + 1] }); i = i + 1; }
    let t: i32 = 0;
    for p in ps { t = t + total(p); }
    return t;
}
`
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		bin := filepath.Join(dir, "stage2_prog")
		compile(t, stage2, src, bin)
		run := exec.Command(bin)
		_ = run.Run()
		// 5 rounds of (2i + 1) + 2: 25 + 10.
		if code := run.ProcessState.ExitCode(); code != 35 {
			t.Fatalf("stage-2-built program exited %d, want 35", code)
		}
	})

	// The checker stage 1 lowered read a freed Map type here (#10832).
	t.Run("check-map-literal-branches", func(t *testing.T) {
		src := filepath.Join(dir, "stage2_check.fern")
		prog := `import "core/map";
function id[T](x: T): T { return x; }
function main(): i32 {
    let a: i32 = 1;
    let v3: Map[i32, i32] = if (false) { Map { a: 1 } } else { Map { id(a): 2 } };
    return 0;
}
`
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if b, err := exec.Command(stage2, "-check", src, stdlibRoot).CombinedOutput(); err != nil {
			t.Fatalf("stage 2 -check: %v\n%s", err, b)
		}
	})

	t.Run("stage3", func(t *testing.T) {
		// CI-DARK: FERN_STAGE3 — doubles a job already ~4 minutes long; stage 2
		// compiling lexer.fern and a program is what runs on every push.
		if os.Getenv("FERN_STAGE3") != "1" {
			t.Skip("set FERN_STAGE3=1 to have stage 2 rebuild the whole compiler")
		}
		compile(t, stage2, filepath.Join(dir, "fern.fern"), filepath.Join(dir, "fern_stage3"))
	})
}
