package e2eselfhost

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
// irlower dispatches a method call as `<receiver-type>.<field>`, and
// `expr_recv_prim_type` used to end by falling through to `expr_scalar_type`,
// whose last resort is `return "i32"`. An array receiver that reached there —
// which happens whenever the `__arrm_<m>` fold declines the method — therefore
// keyed `i32.<m>`. Two outcomes, and only one of them was safe:
//
//   - no such symbol      -> strict-IR names the bail site. Fine.
//   - a symbol EXISTS     -> it is called, with an array pointer as the
//     integer receiver. Wrong answer, no diagnostic,
//     because strict-IR is satisfied by the symbol
//     resolving.
//
// Which one you got depended on whether std/i32 happened to own a verb of that
// name: `rotate_left` and `step_by` have identical signature shapes and failed
// in opposite ways for exactly that reason (#6915 found it on `rotate_left`,
// where the call landed in std/i32's BITWISE rotate).
//
// The case below pins the silent branch, since the loud one was never the
// problem. `pow` is chosen because std/i32 defines `(n: i32) pow(e: i32): i32`
// — a real symbol to be captured by — while `std/array` does not, so the name
// is free for a user method. The bounded EXTRA type param `U` is what makes
// `is_generic_array_method` decline the fold (only the receiver's own element
// var may appear bounded), which is what routes the call into the dispatch.
//
// Before the fix this program compiled clean under FERN_STRICT_IR and returned
// 0 where the interpreter says 103. The assertion is therefore that the module
// is NOT IR-eligible: refusing is the correct outcome, and an answer that
// disagrees with the oracle is the bug.
const arrayRecvMisdispatchSrc = `import "std/i32";
import "core/cmp";

function (xs: T[]) pow[T: cmp.Ord, U: cmp.Ord](u: U): i32 {
    return xs.len() + 100;
}

function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    return xs.pow(2);
}
`

func TestSelfHostArrayRecvMisdispatchRefuses(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host driver runs natively only")
	}
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
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
	if got != "ast" {
		t.Fatalf("-decide = %q, want \"ast\" (module refuses to lower)", got)
	}
}
