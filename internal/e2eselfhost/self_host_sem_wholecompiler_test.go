package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSelfHostSemanticWholeCompilerX86_64 is the whole-compiler gate for the
// semantic lowering (docs/SELFHOST-SEMANTIC-SOURCE.md, #9415): the self-host
// compiler built THROUGH the semantic path has to be a compiler, and the same
// one.
//
// Two things are pinned, in the order they are cheapest to lose:
//
//   - Coverage. gen1 is fern.fern compiled by the native-built driver with
//     FERN_SEM_IR=1, and its report must say every declaration produced. A
//     body that stops producing keeps the AST lowering and the module becomes
//     mixed, which is where every crash in this path's history lived; the
//     tally is the one line that says whether that happened.
//   - Output. gen1 compiles lexer.fern, parser.fern, checker.fern and the
//     whole tree byte-identically to the driver, whose lowering of the same
//     inputs is the AST one. This is the primary gate: a fixpoint is blind to
//     a stable miscompile, this is not.
//
// The fixpoint — gen1 rebuilding itself through the same path — is not here
// yet: measured 2026-09-16 it exhausts the arena 19 minutes in (exit 125),
// a memory defect in the produced code of the semantic modules that the doc
// records. It joins this test when it holds.
//
// Measured 2026-09-16 on a 4-core x86-64 container: gen1 9m26s at 8.4 GB,
// then about 2.5 minutes for the four emits by both compilers. It runs in a
// job of its own (OWN_JOB_TESTS), not in a shard.
func TestSelfHostSemanticWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := filepath.Abs("../../examples/self_host")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(tree, "fern.fern")
	work := t.TempDir()

	gen1 := filepath.Join(work, "fern-gen1")
	report := semSelfBuild(t, driver, entry, stdlibRoot, gen1)
	produced, total := semTally(t, report)
	if produced != total {
		t.Fatalf("gen1 produced %d of %d declarations; every refusal is a body the AST lowering emits into a mixed module:\n%s",
			produced, total, report)
	}

	for _, m := range []string{"lexer", "parser", "checker", "fern"} {
		src := filepath.Join(tree, m+".fern")
		want := emitAsm(t, driver, src, stdlibRoot, filepath.Join(work, m+"-driver.s"))
		got := emitAsm(t, gen1, src, stdlibRoot, filepath.Join(work, m+"-gen1.s"))
		if !bytes.Equal(got, want) {
			t.Fatalf("gen1 compiles %s.fern differently from the AST-lowered driver (%d bytes against %d)", m, len(got), len(want))
		}
	}
}

// semSelfBuild compiles entry to a linked x86-64 binary at out through the
// semantic lowering, and returns the compiler's report.
func semSelfBuild(t *testing.T, compiler, entry, stdlibRoot, out string) string {
	t.Helper()
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-o", out, entry, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s building %s through the semantic lowering: %v\n%s", filepath.Base(compiler), filepath.Base(entry), err, stderr.String())
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	return stderr.String()
}

var semTallyLine = regexp.MustCompile(`module: produced (\d+) of (\d+) declarations`)

// semTally reads the produced and total declaration counts out of the report.
func semTally(t *testing.T, report string) (int, int) {
	t.Helper()
	m := semTallyLine.FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("no production tally in the report:\n%s", report)
	}
	var produced, total int
	for i, dst := range []*int{&produced, &total} {
		for _, c := range m[i+1] {
			*dst = *dst*10 + int(c-'0')
		}
	}
	return produced, total
}

// emitAsm compiles src to x86-64 assembly text with the semantic lowering
// OFF, so the two compilers are compared on the lowering they both carry.
func emitAsm(t *testing.T, compiler, src, stdlibRoot, out string) []byte {
	t.Helper()
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-emit", "asm", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s on %s: %v\n%s", filepath.Base(compiler), filepath.Base(src), err, msg)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(asm) == 0 {
		t.Fatalf("%s emitted nothing for %s", filepath.Base(compiler), filepath.Base(src))
	}
	return asm
}
