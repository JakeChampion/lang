package e2eselfhost

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A write that fails is an error. The runtime's `write_file` loop stopped on
// any non-positive write(2) and answered Ok, so a full disk left a short file
// behind a compile that exited 0 — the self-hosted compiler wrote a truncated
// listing that way. /dev/full fails every write with ENOSPC; native and the
// interpreter answer Err here.
const selfHostWriteFileFullSource = `function main(): i32 {
    match (write_file("/dev/full", "hello")) {
        Ok(_) => { print("ok"); return 1; },
        Err(e) => { print("err"); return 0; }
    }
    return 2;
}
`

func TestSelfHostWriteFileReportsFullDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is a Linux device")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostWriteFileFullSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			if got, report, _ := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, ""); got != "0|err\n" {
				t.Fatalf("answered %q, want %q\nreport: %s", got, "0|err\n", report)
			}
		})
	}
}
