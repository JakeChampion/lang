package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

const selfHostFuzzEnv = "FERN_SELFHOST_FUZZ"

func FuzzSelfHostFrontEnd(f *testing.F) {
	if !envflag.On(selfHostFuzzEnv) {
		f.Skip("set " + selfHostFuzzEnv + "=1 to fuzz the self-host front end")
	}
	gcc, runner := x86_64Tooling(f)
	if len(runner) != 0 {
		f.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		f.Fatalf("abs stdlib root: %v", err)
	}

	dir := writeSelfHostAsmProject(f)
	copySelfHostDriver(f, dir, "fern.fern")
	fernBin := buildSelfHostBin(f, gcc, dir, "fern.fern", "fern")

	work := f.TempDir()
	srcPath := filepath.Join(work, "main.fern")

	seeds := []string{
		``,
		`function main(): i32 { return 0; }`,
		`function main(): i32 { let a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function main(): i32 { let s = 0; let i = 0; while (i < 10) { s = s + i; i = i + 1; } return s; }`,
		`struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 1, y: 2 }; return p.x; }`,
		`enum E { A, B } function main(): i32 { match (E.A) { E.A => { return 0; }, E.B => { return 1; } } }`,
		`function main(): i32 { return true; }`,
		`function main(): i32 { return nope; }`,
		`function (`,
		"function main(): i32 { return " + strings.Repeat("(", 3000) + "0" + strings.Repeat(")", 3000) + "; }",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-check", srcPath)
		cmd.Env = append(os.Environ(), "FERN_STDLIB_ROOT="+stdlibRoot)
		r := runSelfHostBin(cmd, "")
		if !r.exited {
			t.Errorf("self-host front end died on a signal (%s) instead of reporting:\n%q\nstdout:\n%s\nstderr:\n%s",
				r.state, src, r.stdout, r.stderr)
		}
		if r.timedOut {
			t.Errorf("self-host front end hung on:\n%q", src)
		}
	})
}
