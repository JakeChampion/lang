package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ownEnumQualifierLib is #8650's reproducer, widened to the shapes the one bug
// showed up in: a module that declares `enum E` and then spells its variants
// `E.Wrap` — in CONSTRUCTOR position with a constant payload (`E.Wrap(3)`) and
// with a variable one (`E.Wrap(n)`), as a payload-less VALUE (`E.Nil`), and in
// PATTERN position (`E.Wrap(k)` / `E.Nil`).
//
// Only an IMPORTED module was affected: flatten prefix-mangles a non-entry
// module's decls, so the enum becomes `lib__E` and its variants `lib__Wrap` /
// `lib__Nil`, but `E.Wrap` was only ever tested against the module-qualifier
// reading. Missing it left the constructor as a field access on a mangled enum
// NAME (bailing IR eligibility at `lib__probe` and taking the whole module to
// `module: AST`) and left the pattern spelled `E.Wrap` against an enum whose
// variants are `lib__Wrap` — so every variant read as uncovered and E030 fired
// on a match covering both.
//
// probe(5) = 1 (the E.Nil arm) + 3 (the constant payload) + 5 (the variable
// one) = 9, which is also what native returns for the same two files.
const ownEnumQualifierLib = `enum E { Wrap(i32), Nil }

pub function probe(n: i32): i32 {
    var a: E = E.Wrap(3);
    var b: E = E.Wrap(n);
    var u: E = E.Nil;
    var t: i32 = 0;
    match (u) {
        E.Wrap(k) => { t = t + k; },
        E.Nil => { t = t + 1; }
    }
    match (a) {
        E.Wrap(k) => { t = t + k; },
        E.Nil => { t = t + 100; }
    }
    match (b) {
        E.Wrap(k) => { t = t + k; },
        E.Nil => { t = t + 200; }
    }
    return t;
}
`

const ownEnumQualifierMain = `import "./lib";
function main(): i32 { return lib.probe(5); }
`

// TestSelfHostOwnEnumQualifierX86_64 pins #8650 end to end through the
// import-driven driver (asm_modload_run.fern), which is the only self-host
// entry point that resolves a real `import` graph and so the only one where
// the imported module gets prefix-mangled at all.
//
// Two assertions, because the bug had two faces and one resolution behind
// them. `-ir-probe` is the eligibility half: it reported
// `lib__probe: BAIL lower call const_func` and `module: AST`. Emitting and
// RUNNING the program is the other half plus the proof the arms still select:
// the checker rejected these files outright with E030, and a fix that merely
// stopped erroring while the qualified arm no longer matched would be worse
// than the bug.
func TestSelfHostOwnEnumQualifierX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	progDir := t.TempDir()
	for name, src := range map[string]string{
		"lib.fern":  ownEnumQualifierLib,
		"main.fern": ownEnumQualifierMain,
	} {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(progDir, "main.fern")

	t.Run("ir-probe", func(t *testing.T) {
		cmd := runX86_64Bin(runner, driverBin, entry, "-ir-probe")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run -ir-probe driver: %v", err)
		}
		report := string(out)
		for _, want := range []string{"lib__probe: ir", "main: ir", "module: IR"} {
			if !strings.Contains(report, want) {
				t.Errorf("probe report missing %q\n--- report ---\n%s", want, report)
			}
		}
	})

	t.Run("runs", func(t *testing.T) {
		progAsm := runDriverFile(t, runner, driverBin, entry)
		if len(progAsm) == 0 {
			t.Fatal("driver emitted 0 bytes")
		}
		progBin := buildBin(t, gcc, progDir, "prog", string(progAsm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
		}
		_, _ = cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 9 {
			t.Errorf("program exited %d, want 9", code)
		}
	})
}

// TestSelfHostOwnEnumQualifierCheckX86_64 is the same construct at the CHECKER
// (checker_modload_run.fern), which is where #8650's second face showed: the
// qualified arms of a match covering both variants each read as uncovered, so
// a correct match drew one E030 per variant.
//
// The mismatched-qualifier half is what proves the fix did not turn the checks
// reading the qualifier into no-ops. `Other.Text` on a `Kind` scrutinee is
// E029; before the fix the qualifier stayed spelled `Other` while the enum
// table held `lib__Other`, so the lookup missed and no qualifier diagnostic
// was reported at all.
func TestSelfHostOwnEnumQualifierCheckX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	check := func(t *testing.T, lib string) (string, int) {
		t.Helper()
		progDir := t.TempDir()
		bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
		if err != nil {
			t.Fatalf("read builtins.fern: %v", err)
		}
		files := map[string][]byte{
			"builtins.fern": bsrc,
			"lib.fern":      []byte(lib),
			"main.fern":     []byte(ownEnumQualifierMain),
		}
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(progDir, name), src, 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		cmd := runX86_64Bin(runner, driverBin, filepath.Join(progDir, "main.fern"))
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	t.Run("clean", func(t *testing.T) {
		out, code := check(t, ownEnumQualifierLib)
		if code != 0 {
			t.Errorf("checker driver exited %d, want 0 (no diagnostics); codes %q", code, out)
		}
		if strings.Contains(out, "E030") {
			t.Errorf("exhaustive match over qualified arms reported E030: %q", out)
		}
	})

	t.Run("mismatched-qualifier", func(t *testing.T) {
		out, code := check(t, `enum Kind { Text, Number }
enum Other { Text, Blah }

pub function probe(n: i32): i32 {
    var k: Kind = Kind.Text;
    match (k) {
        Other.Text => { return 1; },
        Kind.Number => { return 2; }
    }
    return n;
}
`)
		if code != 1 {
			t.Errorf("checker driver exited %d, want 1 (diagnostics emitted); codes %q", code, out)
		}
		if !strings.Contains(out, "E029") {
			t.Errorf("want E029 for `Other.Text` on a Kind scrutinee, got codes %q", out)
		}
	})

	// Keeping the qualifier on the pattern for the payload-TYPE lookups made
	// the record-rebind's struct lookup miss, because that table is keyed by
	// the BARE variant name: rss.variant_field_names came back empty,
	// record_fields_cover answered false, and an enum-qualified record pattern
	// was silently ACCEPTED where native reports E015. Rejecting too much had
	// become accepting too much, which is the direction that breaks the
	// checker differential rather than merely annoying an author.
	t.Run("qualified-record-pattern-is-refused", func(t *testing.T) {
		out, code := check(t, `enum E { Wrap(i32, i32), Nil }

pub function probe(n: i32): i32 {
    var v: E = E.Wrap(1, 2);
    match (v) {
        E.Wrap { x: k, y: j } => { return k + j; },
        E.Nil => { return 0; }
    }
    return n;
}
`)
		if code != 1 {
			t.Errorf("checker driver exited %d, want 1: a record pattern on a POSITIONAL "+
				"variant is E015 whether or not the pattern carries its enum qualifier; "+
				"codes %q", code, out)
		}
		if !strings.Contains(out, "E015") {
			t.Errorf("want E015 for `E.Wrap { x: k, y: j }` on a positional variant, got codes %q", out)
		}
	})

	// #8783: the payload TYPE lookups are keyed by the bare variant name too,
	// so a qualified `E.Wrap` missed and the payload bound as UNKNOWN — every
	// downstream check on it was skipped and the arm passed in silence. The
	// bare spelling of the same program is E038, and so is native on both.
	// Resolution is scoped through the enum owner, not a bare strip, so
	// shared-name variants (below) still each answer for their own decl.
	payloadMismatchLib := func(pat string) string {
		return `enum E { Wrap(i32), Nil }

function want_str(s: string): i32 { return s.len(); }

pub function probe(n: i32): i32 {
    var v: E = E.Wrap(7);
    match (v) {
        ` + pat + ` => { return want_str(k); },
        E.Nil => { return 0; }
    }
    return n;
}
`
	}

	t.Run("qualified-payload-type-is-checked", func(t *testing.T) {
		out, code := check(t, payloadMismatchLib("E.Wrap(k)"))
		if code != 1 || !strings.Contains(out, "E038") {
			t.Errorf("qualified `E.Wrap(k)`: exit %d codes %q; want exit 1 with E038 — "+
				"an i32 payload passed to a string parameter, exactly as the bare "+
				"spelling and native both report", code, out)
		}
	})

	t.Run("bare-payload-type-is-checked", func(t *testing.T) {
		out, code := check(t, payloadMismatchLib("Wrap(k)"))
		if code != 1 || !strings.Contains(out, "E038") {
			t.Errorf("bare `Wrap(k)`: exit %d codes %q; want exit 1 with E038", code, out)
		}
	})

	// The 2nd+ payload goes through variant_payload_type_at rather than
	// variant_binding_type; it missed on a qualified name the same way.
	t.Run("qualified-extra-payload-type-is-checked", func(t *testing.T) {
		out, code := check(t, `enum E { Pair(i32, i32), Nil }

function want_str(s: string): i32 { return s.len(); }

pub function probe(n: i32): i32 {
    var v: E = E.Pair(1, 2);
    match (v) {
        E.Pair(a, b) => { return a + want_str(b); },
        E.Nil => { return 0; }
    }
    return n;
}
`)
		if code != 1 || !strings.Contains(out, "E038") {
			t.Errorf("qualified `E.Pair(a, b)`: exit %d codes %q; want exit 1 with E038 "+
				"on the SECOND payload binding", code, out)
		}
	})

	// The other direction, and the one a naive strip breaks: two enums each
	// declaring a variant `W` with a different payload type. Resolution has to
	// answer per OWNER — a bare lookup answers for whichever `W` was declared
	// first, which is conformance/cases/shared_variant_payload's regression.
	// `B.W`'s payload is a string, so `want_i32(s)` is E038 while `A.W`'s i32
	// payload through the same helper is clean.
	t.Run("shared-variant-name-resolves-per-owner", func(t *testing.T) {
		const sharedLib = `enum A { W(i32), P }
enum B { W(string), Q }

function want_i32(v: i32): i32 { return v; }

pub function probe(n: i32): i32 {
    var a: A = A.W(1);
    var b: B = B.W("hi");
    var t: i32 = 0;
    match (a) {
        A.W(k) => { t = t + want_i32(k); },
        A.P => { t = t + 1; }
    }
    match (b) {
        B.W(s) => { t = t + PROBE; },
        B.Q => { t = t + 2; }
    }
    return t + n;
}
`
		t.Run("second-owner-payload-is-its-own-type", func(t *testing.T) {
			out, code := check(t, strings.Replace(sharedLib, "PROBE", "s.len()", 1))
			if code != 0 {
				t.Errorf("checker exited %d, want 0: `B.W(s)` binds a string, so "+
					"`s.len()` resolves; codes %q", code, out)
			}
		})
		t.Run("second-owner-payload-is-not-the-first-owners", func(t *testing.T) {
			out, code := check(t, strings.Replace(sharedLib, "PROBE", "want_i32(s)", 1))
			if code != 1 || !strings.Contains(out, "E038") {
				t.Errorf("`B.W(s)` passed to an i32 parameter: exit %d codes %q; want "+
					"exit 1 with E038 — reading A's i32 for B's `W` is the "+
					"shared_variant_payload regression", code, out)
			}
		})
	})

	// The payload COUNT is resolved off the same decl as the payload types, so
	// it failed open on a qualified name exactly as the types did: a binding
	// count that does not match the variant's payload count is E015 for the
	// bare spelling and for native, and was accepted in silence when the
	// pattern carried its enum qualifier.
	arityLib := func(pat string) string {
		return `enum E { Pair(i32, i32), Nil }

pub function probe(n: i32): i32 {
    var v: E = E.Pair(1, 2);
    match (v) {
        ` + pat + ` => { return a; },
        E.Nil => { return 0; }
    }
    return n;
}
`
	}

	t.Run("qualified-payload-arity-is-checked", func(t *testing.T) {
		out, code := check(t, arityLib("E.Pair(a)"))
		if code != 1 || !strings.Contains(out, "E015") {
			t.Errorf("qualified `E.Pair(a)`: exit %d codes %q; want exit 1 with E015 — "+
				"one binding for two payloads, exactly as the bare spelling and "+
				"native both report", code, out)
		}
	})

	t.Run("bare-payload-arity-is-checked", func(t *testing.T) {
		out, code := check(t, arityLib("Pair(a)"))
		if code != 1 || !strings.Contains(out, "E015") {
			t.Errorf("bare `Pair(a)`: exit %d codes %q; want exit 1 with E015", code, out)
		}
	})

	// And the other direction for the count: two enums whose same-named
	// variants have DIFFERENT arities. Reading the first-declared decl made
	// `B.W(1, 2)` an E036 "expects 1 argument(s), got 2" against A's arity,
	// on a program native accepts.
	t.Run("shared-variant-name-arity-resolves-per-owner", func(t *testing.T) {
		out, code := check(t, `enum A { W(i32), P }
enum B { W(i32, i32), Q }

pub function probe(n: i32): i32 {
    var b: B = B.W(1, 2);
    match (b) {
        B.W(x, y) => { return x + y + n; },
        B.Q => { return 0; }
    }
    return n;
}
`)
		if code != 0 {
			t.Errorf("checker exited %d, want 0: `B.W` takes two payloads whatever "+
				"arity A's `W` was declared with; codes %q", code, out)
		}
	})
}
