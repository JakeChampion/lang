package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostAssumeEligibleBailRefusesByName pins what a lowering bail looks
// like on the routes with no eligibility gate in front of the emit (#8590).
//
// `-assume-eligible` skips the pre-check by design, so a function that bails
// reaches the backend as a failed LowerResult: a PARTIAL op stream with
// n_locals 0. Handing that to the IR verify gate produced
//
//	FERN_IR_VERIFY: main lowered to malformed IR (#6639)
//	    ... local index 1 is outside the frame (0 params + 0 locals = 0 slots)
//
// which reads as a verifier or slot-accounting defect and discards the bail's
// own reason — every bail on this path presented that way, and #8585 spent a
// five-way bisect learning that a new bail in a destructure lowering was the
// trigger. The emit must instead refuse the function by name with the reason
// the lowering recorded, exactly as the checked route does.
//
// The programs are ones the AST lowering refuses; a row that starts lowering
// is replaced by another refusal, not weakened. Each row runs on both
// register targets — the driver is one x86-64 binary, and `-target arm64-linux`
// only selects which emitter the partial stream would have reached — and once
// through the batched emit-all route the whole-compiler build takes.
func TestSelfHostAssumeEligibleBailRefusesByName(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "ae_bail_driver")
	// The bail is the AST lowering's: the per-module routes lower the whole
	// program through the typed lowering, which gates it before any unit. Set
	// after the build, which stage0 would otherwise run on the AST lowering.
	t.Setenv("FERN_SEM_IR", "")

	// One entry per fixture: the per-module route resolves imports from the
	// entry's directory, and these programs have none.
	proj := t.TempDir()
	entryFor := func(t *testing.T, tc struct{ name, src, fn, reason string }) string {
		t.Helper()
		p := filepath.Join(proj, tc.name+".fern")
		if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		return p
	}

	// A refusal names the function and the construct, exits 3 like every
	// other IR refusal, and is not the verifier's account of the malformed IR.
	assertRefusal := func(t *testing.T, leg string, stderr string, code int, fn, reason string) {
		t.Helper()
		if code != 3 {
			t.Fatalf("%s: exited %d, want 3 (a refusal)\n%s", leg, code, stderr)
		}
		if strings.Contains(stderr, "FERN_IR_VERIFY") || strings.Contains(stderr, "malformed IR") {
			t.Fatalf("%s: the bail was reported as malformed IR — the partial op stream reached the verify gate\n%s", leg, stderr)
		}
		if !strings.Contains(stderr, fn+" ") {
			t.Errorf("%s: refusal does not name the bailing function %q\n%s", leg, fn, stderr)
		}
		if !strings.Contains(stderr, reason) {
			t.Errorf("%s: refusal does not carry the bail's reason %q\n%s", leg, reason, stderr)
		}
	}

	for _, tc := range astBailReasons {
		t.Run(tc.name, func(t *testing.T) {
			entry := entryFor(t, tc)
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				out, stderr, code := runDriver(t, runner, driverBin, nil, true,
					entry, "-per-module-emit", "0", "-assume-eligible", "-target", target)
				if len(out) != 0 {
					t.Errorf("%s: emitted %d bytes for a function that did not lower", target, len(out))
				}
				assertRefusal(t, target+" strict", stderr, code, tc.fn, tc.reason)
			}
			// The refusal does not depend on FERN_STRICT_IR: there is nothing
			// to fall back to, so it is unconditional, and the flag only ever
			// selected which diagnostic the CHECKED route printed.
			_, stderr, code := runDriver(t, runner, driverBin, nil, false,
				entry, "-per-module-emit", "0", "-assume-eligible")
			assertRefusal(t, "x86-64-linux unset", stderr, code, tc.fn, tc.reason)
		})
	}

	// The batched route (`-per-module-emit-all -assume-eligible`) is the one
	// every whole-compiler build takes, and the one #8585 hit.
	t.Run("emit-all", func(t *testing.T) {
		tc := astBailReasons[0]
		entry := entryFor(t, tc)
		outDir := filepath.Join(t.TempDir(), "units")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		_, stderr, code := runDriver(t, runner, driverBin, nil, true,
			entry, "-per-module-emit-all", "-out-dir", outDir, "-assume-eligible")
		assertRefusal(t, "emit-all strict", stderr, code, tc.fn, tc.reason)
	})
}

var astBailReasons = []struct{ name, src, fn, reason string }{
	// A loop over the innermost level of a 4-deep nested array.
	{"nested-for", `function main(): i32 {
    var hyper: i32[][][][] = [[[[1]], [[2, 3]]]];
    var sum = 0;
    for cube in hyper { for plane in cube { for row in plane { for v in row { sum = sum + v; } } } }
    return sum;
}
`, "main", "did not lower: `for v`"},
	// A value-position match desugars to an immediately invoked value block; an
	// 8-byte-element array payload is what it cannot bind.
	{"iife-value-block", `enum W { Wide(i64[]), Empty }
function main(): i32 {
    var w: W = Wide([5i64, 6i64]);
    var u: i64 = (match (w) { Wide(xs) => xs[0], Empty => 9i64 });
    return (u as i32) & 255i32;
}
`, "main", "did not lower: `var u` bound from immediately-invoked value block"},
}
