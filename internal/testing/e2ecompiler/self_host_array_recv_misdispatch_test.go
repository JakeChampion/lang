package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An array-method call the monomorphiser did NOT rewrite must refuse, not
// dispatch as `i32.<method>`.
//
// A receiver whose type the lowering cannot name must not fall back to `i32`.
// Where std/i32 owns a verb of that name, the call resolves — with an array
// pointer as the integer receiver — and the answer is wrong with no
// diagnostic, because strict-IR is satisfied by the symbol resolving. #6915
// found it on `rotate_left`, where the call landed in std/i32's BITWISE rotate.
//
// The case below pins that silent branch. `pow` is chosen because std/i32
// defines `(n: i32) pow(e: i32): i32` — a real symbol to be captured by — while
// `std/array` does not, so the name is free for a user method. The bounded
// EXTRA type param `U` is what makes `is_generic_array_method` decline the fold
// (only the receiver's own element var may appear bounded), which is what
// routes the call into the dispatch.
//
// The interpreter answers 103. The assertion is that the module is NOT
// IR-eligible: refusing is the correct outcome, and an answer that disagrees
// with the oracle is the bug.
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

	// The native interpreter is the oracle for what this program means.
	_, want := runFixtureInterp(t, entry, "")
	if want != 103 {
		t.Fatalf("oracle = %d, want 103 (the case stopped exercising what it describes)", want)
	}

	route, _ := exec.Command(driver, entry, root, "-decide").Output()
	got := strings.TrimSpace(string(route))
	if got == "ir" {
		// Would have been the silent miscompile: lowered, wrong answer.
		t.Fatalf("-decide = \"ir\": the unrewritten array-method call lowered anyway, "+
			"which means it dispatched as i32.pow and captured std/i32's integer pow "+
			"instead of the receiver's method (oracle says %d)", want)
	}
	if got != "refused" {
		t.Fatalf("-decide = %q, want \"refused\" (module refuses to lower)", got)
	}
}
