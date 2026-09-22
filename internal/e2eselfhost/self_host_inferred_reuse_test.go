package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// inferredReuseModules are compiler modules whose emitted code must not depend
// on whether the second lowering re-planned every row.
//
// Every one of them is a module the typed pipeline lowers WHOLE, which is the
// only configuration the reuse can be reached in: `semlower.inferred_pass`
// runs the ownership inference only after `lowers_whole_module` says the first
// pass produced every body, so a mixed module never gets a second lowering at
// all and cannot exercise this. They are also the modules the self-compile is
// made of, which is where the saving is worth having.
var inferredReuseModules = []string{
	// The two the diagnosis was measured on, smallest first so a failure is
	// cheapest to read.
	"lexer.fern",
	"checker.fern",
	// A module whose declarations call each other heavily, so the grow-mask
	// dependency below is actually loaded: the reuse must notice a callee whose
	// mask moved even where the caller's own modes did not.
	"ssarc.fern",
	// Where the analyses that were running twice live.
	"ssaunits.fern",
	"semsource.fern",
}

// TestSelfHostInferredReuseIsIdentical holds the #9969 reuse to byte equality
// with re-lowering everything.
//
// `semlower.rows_of` takes back the plan and the body of every row the
// ownership inference did not move, rather than planning and lowering the whole
// module a second time. Two claims make that sound, and neither is checkable by
// reading the emitted code for a marker:
//
//   - a plan is a function of its row's graph and modes alone, so an unmoved
//     row plans identically;
//   - a body also reads the module's grow table, but only through the callees
//     it names (`ssarc.bracketed` keys `grow_fields_of` on the call
//     instruction's `str`), so an unmoved row lowers identically exactly when
//     those callees' rows agree.
//
// The second claim is the one that can be wrong quietly. A moved row can widen
// a callee's grow mask, and a caller that brackets that callee then needs
// re-lowering although its own modes are untouched — get that wrong and the
// bracket is dropped, which is a use-after-free in the compiled program and not
// a refusal. So the gate compares the WHOLE emitted module against the
// re-lowering, which cannot miss a dropped bracket the way a grep for a helper
// name can.
//
// `FERN_SEM_REUSE=` is the off column. Without it the reuse would be a fast
// path with no slow path to check.
func TestSelfHostInferredReuseIsIdentical(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	selfHostRoot, err := filepath.Abs("../../examples/self_host")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, mod := range inferredReuseModules {
		t.Run(mod, func(t *testing.T) {
			src := filepath.Join(selfHostRoot, mod)
			reused := emitSelfHostAsm(t, runner, fernBin, stdlibRoot, src, "reused", nil)
			relowered := emitSelfHostAsm(t, runner, fernBin, stdlibRoot, src, "relowered", []string{"FERN_SEM_REUSE="})
			// An empty or absent output would compare equal to another empty
			// one, so the gate would pass having measured nothing.
			if len(reused) == 0 {
				t.Fatalf("%s: the reuse pass emitted nothing, so this comparison proves nothing", mod)
			}
			if len(relowered) == 0 {
				t.Fatalf("%s: the re-lowering pass emitted nothing, so this comparison proves nothing", mod)
			}
			if !bytes.Equal(reused, relowered) {
				t.Errorf("%s: emitted code differs between taking back the rows the inference did not move and "+
					"re-lowering every row (%d bytes reused, %d bytes re-lowered). A row was kept whose lowering "+
					"the inference did move — most likely a caller whose own modes are untouched but whose callee's "+
					"grow mask widened, which drops a bracket rather than refusing. %s",
					mod, len(reused), len(relowered), firstAsmDifference(reused, relowered))
			}
		})
	}
}

// emitSelfHostAsm compiles one module to assembly text with the self-host CLI
// and returns the bytes. `extraEnv` is appended to the process environment, so
// a variable it sets wins over an inherited one.
func emitSelfHostAsm(t *testing.T, runner []string, fernBin, stdlibRoot, src, tag string, extraEnv []string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), tag+".s")
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), extraEnv...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile (%s): %v\n%s", tag, err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read asm (%s): %v", tag, err)
	}
	return asm
}

// firstAsmDifference names the first line the two emissions disagree on, so a
// failure points at a function instead of a byte offset in a megabyte of text.
func firstAsmDifference(a, b []byte) string {
	la, lb := bytes.Split(a, []byte("\n")), bytes.Split(b, []byte("\n"))
	for i := 0; i < len(la) && i < len(lb); i++ {
		if !bytes.Equal(la[i], lb[i]) {
			return "First difference at line " + strconv.Itoa(i+1) + ":\n    reused:      " + string(la[i]) +
				"\n    re-lowered:  " + string(lb[i]) + "\n" + nearestLabelBefore(la, i)
		}
	}
	if len(la) != len(lb) {
		return "The two emissions agree line for line up to line " + strconv.Itoa(min(len(la), len(lb))) +
			" and then one ends: " + strconv.Itoa(len(la)) + " lines reused against " + strconv.Itoa(len(lb)) + " re-lowered."
	}
	return ""
}

// nearestLabelBefore names the function a differing line sits in, reading back
// to the nearest label rather than reporting a bare line number.
func nearestLabelBefore(lines [][]byte, at int) string {
	for i := at; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if bytes.HasSuffix(line, []byte(":")) && bytes.HasPrefix(line, []byte("__fn_")) {
			return "    in " + string(bytes.TrimSuffix(line, []byte(":")))
		}
	}
	return "    before the first function label"
}
