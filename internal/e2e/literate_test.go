package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A literate `.fern.md` document — Knuth-style named chunks, defined
// out of narrative order — tangles and runs end to end through
// `fern -interp`. The root chunk pulls the helper in *before* it is
// textually defined, which is the whole point of named chunks.
const factLiterate = "# Factorial\n" +
	"\n" +
	"The program is assembled from named chunks:\n" +
	"\n" +
	"```fern\n" +
	"<<*>>=\n" +
	"<<imports>>\n" +
	"<<fact>>\n" +
	"<<main>>\n" +
	"```\n" +
	"\n" +
	"It needs the integer helpers in scope:\n" +
	"\n" +
	"```fern\n" +
	"<<imports>>=\n" +
	"import \"core/no_prelude\";\n" +
	"import \"std/i32\";\n" +
	"```\n" +
	"\n" +
	"`main` computes 5! and reports it:\n" +
	"\n" +
	"```fern\n" +
	"<<main>>=\n" +
	"function main(): i32 {\n" +
	"    var f: i32 = fact(5);\n" +
	"    print(f.to_string());\n" +
	"    return f;\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"The recursion lives in its own chunk, defined last:\n" +
	"\n" +
	"```fern\n" +
	"<<fact>>=\n" +
	"function fact(n: i32): i32 {\n" +
	"    if (n == 0) { return 1; }\n" +
	"    return n * fact(n - 1);\n" +
	"}\n" +
	"```\n"

// `fern -interp FILE.fern.md` tangles the literate document in memory
// and runs it: out-of-order chunks resolve, main() returns 5! = 120,
// and that becomes the exit code.
func TestLiterateInterpEndToEnd(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fact.fern.md")
	if err := os.WriteFile(src, []byte(factLiterate), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 120 {
		t.Errorf("exit = %d, want 120 (5!)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "120") {
		t.Errorf("stdout missing `120`: %q", out.String())
	}
}

// `fern -tangle FILE.fern.md` writes plain Fern source with the chunks
// expanded in dependency order (imports, then fact, then main).
func TestLiterateTangleStdout(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fact.fern.md")
	if err := os.WriteFile(src, []byte(factLiterate), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-tangle", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("tangle failed: %v\nstderr: %s", err, errb.String())
	}
	got := out.String()
	// No literate machinery leaks into the tangled source.
	if strings.Contains(got, "<<") || strings.Contains(got, "# Factorial") {
		t.Errorf("tangled output still contains literate markup:\n%s", got)
	}
	// fact must be defined before main uses it (dependency order).
	if i, j := strings.Index(got, "function fact"), strings.Index(got, "function main"); i < 0 || j < 0 || i > j {
		t.Errorf("expected fact defined before main in tangled output:\n%s", got)
	}
}

// `fern -weave FILE.fern.md` writes a cross-referenced Markdown reading
// document: prose survives, chunks get ⟨name⟩≡ labels, references show
// as ⟨ref⟩, and a "used in" footer cross-links them.
func TestLiterateWeaveStdout(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fact.fern.md")
	if err := os.WriteFile(src, []byte(factLiterate), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-weave", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("weave failed: %v\nstderr: %s", err, errb.String())
	}
	got := out.String()
	for _, want := range []string{"# Factorial", "⟨*⟩≡", "⟨fact⟩≡", "⟨fact⟩ is used in ⟨*⟩"} {
		if !strings.Contains(got, want) {
			t.Errorf("woven output missing %q:\n%s", want, got)
		}
	}
}

// A type error inside a chunk is reported against the line the author
// wrote in the `.fern.md` document — not the line in the generated,
// reordered source. This is the literate diagnostic-remapping contract.
func TestLiterateDiagnosticPointsAtDocumentLine(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	// The offending line ("var f: i32 = "oops";") is deliberately late
	// in the document but, after tangling, would land early in the
	// generated source — so a naive (non-remapped) diagnostic would
	// report the wrong line.
	doc := "# Buggy\n" + // line 1
		"\n" + // 2
		"```fern\n" + // 3
		"<<*>>=\n" + // 4
		"<<main>>\n" + // 5
		"```\n" + // 6
		"\n" + // 7
		"```fern\n" + // 8
		"<<main>>=\n" + // 9
		"function main(): i32 {\n" + // 10
		"    var f: i32 = \"oops\";\n" + // 11  <-- type error here
		"    return f;\n" + // 12
		"}\n" + // 13
		"```\n" // 14
	src := filepath.Join(dir, "buggy.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected -check to fail on a type error\nstdout: %s", out.String())
	}
	msg := errb.String()
	// The diagnostic must reference the document file and the document
	// line (11), and render the line the author actually wrote.
	if !strings.Contains(msg, "buggy.fern.md:11:") {
		t.Errorf("diagnostic should point at buggy.fern.md line 11, got:\n%s", msg)
	}
	if !strings.Contains(msg, `var f: i32 = "oops";`) {
		t.Errorf("diagnostic should render the document source line, got:\n%s", msg)
	}
}
