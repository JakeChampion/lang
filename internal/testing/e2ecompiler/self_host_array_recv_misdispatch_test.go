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

function (xs: T[]) pow[T: cmp.Ord, U: cmp.Ord](u: U): i32 {
    return xs.len() + 100;
}

function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    return xs.pow(2);
}
`

func TestSelfHostArrayRecvMisdispatchRefuses(t *testing.T) {
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
	got := strings.TrimSpace(string(route))
	if got == "ir" {
		t.Fatalf("-decide = \"ir\": the unrewritten array-method call lowered anyway, "+
			"which means it dispatched as i32.pow and captured std/i32's integer pow "+
			"instead of the receiver's method (oracle says %d)", want)
	}
	if got != "refused" {
		t.Fatalf("-decide = %q, want \"refused\" (module refuses to lower)", got)
	}
}
