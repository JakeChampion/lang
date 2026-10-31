package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const arrayRecvMisdispatchSrc = `import "std/i32";
import "core/cmp";

function (xs: (T: cmp.Ord)[]) pow[U: cmp.Ord](u: U): i32 {
    return xs.len() + 100;
}

function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    return xs.pow(2);
}
`

func TestSelfHostArrayRecvExtraTypeParamDispatches(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host driver runs natively only")
	}
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	entry := filepath.Join(dir, "arr_recv_misdispatch.fern")
	if err := os.WriteFile(entry, []byte(arrayRecvMisdispatchSrc), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	_, want := runFixtureInterp(t, entry, "")
	if want != 103 {
		t.Fatalf("oracle = %d, want 103 (the case stopped exercising what it describes)", want)
	}

	route, _ := exec.Command(driver, entry, root, "-decide").Output()
	if got := strings.TrimSpace(string(route)); got != "ir" {
		t.Fatalf("-decide = %q, want \"ir\"", got)
	}
	asm, _ := exec.Command(driver, entry, root).Output()
	bin := buildBin(t, gcc, dir, "arr_recv_misdispatch_bin", string(asm))
	cmd := exec.Command(bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Fatalf("self-host run = %d, want %d: the call reached std/i32's integer pow, not the receiver's method", code, want)
	}
}
