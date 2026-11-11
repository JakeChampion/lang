package e2eharness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOracleVerdictCachesByInputs(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	fake := filepath.Join(work, "fake-oracle")
	script := "#!/bin/sh\necho run >> " + calls + "\necho \"verdict for $2\"\necho \"note from $1\" >&2\nexit 3\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(work, "main_test.fern")
	helper := filepath.Join(work, "helper.fern")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(src, "import \"./helper\";\nimport \"std/test\";\nfunction main(): i32 { return 0; }\n")
	write(helper, "pub function one(): i32 { return 1; }\n")
	runs := func() int {
		data, _ := os.ReadFile(calls)
		return strings.Count(string(data), "run")
	}
	check := func(wantRuns int) {
		t.Helper()
		out, exit := InterpVerdict(t, fake, src)
		if exit != 3 || string(out) != "verdict for "+src+"\n" {
			t.Fatalf("verdict = %q exit %d, want the fake's output and exit 3", out, exit)
		}
		if got := runs(); got != wantRuns {
			t.Fatalf("interpreter ran %d times, want %d", got, wantRuns)
		}
	}

	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	check(1)
	check(1)
	write(helper, "pub function one(): i32 { return 2; }\n")
	check(2)
	check(2)
	t.Setenv("FERN_INTERP_VERDICT_PROBE", "on")
	check(3)
	t.Setenv("FERN_STDTEST_SHARD", "5/8")
	check(3)
	t.Setenv("FERN_STDTEST_SHARD", "2/8")
	check(3)

	out, exit := CheckVerdict(t, fake, src)
	if exit != 3 || !strings.Contains(string(out), "verdict for "+src) || !strings.Contains(string(out), "note from -check") {
		t.Fatalf("check verdict = %q exit %d, want stdout and stderr together and exit 3", out, exit)
	}
	if got := runs(); got != 4 {
		t.Fatalf("-check shared the -interp verdict: %d runs, want 4", got)
	}
	CheckVerdict(t, fake, src)
	if got := runs(); got != 4 {
		t.Fatalf("a second -check ran the oracle again: %d runs, want 4", got)
	}

	expectRuns := func(want int, what string) {
		t.Helper()
		if got := runs(); got != want {
			t.Fatalf("%s: the oracle ran %d times, want %d", what, got, want)
		}
	}
	CheckTargetVerdict(t, fake, src, TargetWasm32Wasi)
	expectRuns(5, "-check -target shared the -check verdict")
	CheckTargetVerdict(t, fake, src, TargetWasm32Wasi)
	expectRuns(5, "a second -check -target wasm32-wasi")
	CheckTargetVerdict(t, fake, src, TargetX86_64Linux)
	expectRuns(6, "a second target shared the first target's verdict")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, src)
	if err != nil {
		t.Fatal(err)
	}
	CheckTargetVerdict(t, fake, rel, TargetWasm32Wasi)
	expectRuns(7, "a relative spelling, which the output names, shared the absolute one's verdict")

	t.Setenv("FERN_SELFHOST_BUILD_CACHE", "")
	check(8)
	check(9)
}

func TestOracleVerdictKeysAMissingImport(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	fake := filepath.Join(work, "fake-oracle")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho run >> "+calls+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(work, "main.fern")
	if err := os.WriteFile(src, []byte("import \"./missing\";\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := func() int {
		data, _ := os.ReadFile(calls)
		return strings.Count(string(data), "run")
	}
	CheckVerdict(t, fake, src)
	CheckVerdict(t, fake, src)
	if got := runs(); got != 1 {
		t.Fatalf("a program with a missing import ran the oracle %d times, want 1", got)
	}
	if err := os.WriteFile(filepath.Join(work, "missing.fern"), []byte("pub function f(): i32 { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	CheckVerdict(t, fake, src)
	if got := runs(); got != 2 {
		t.Fatalf("the missing module appeared and the oracle ran %d times, want 2", got)
	}
}

func TestOracleVerdictKeysASourceThatDoesNotLex(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	fake := filepath.Join(work, "fake-oracle")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho run >> "+calls+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(work, "main.fern")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runs := func() int {
		data, _ := os.ReadFile(calls)
		return strings.Count(string(data), "run")
	}
	write("function main(): i32 { return 1_; }\n")
	CheckVerdict(t, fake, src)
	CheckVerdict(t, fake, src)
	if got := runs(); got != 1 {
		t.Fatalf("a source that does not lex ran the oracle %d times, want 1", got)
	}
	write("function main(): i32 { return 2_; }\n")
	CheckVerdict(t, fake, src)
	if got := runs(); got != 2 {
		t.Fatalf("the source changed and the oracle ran %d times, want 2", got)
	}
}

func TestCheckSourceVerdictKeysTheSource(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	fake := filepath.Join(work, "fake-oracle")
	script := "#!/bin/sh\necho run >> " + calls + "\necho \"args $1 $2\"\ncat\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runs := func() int {
		data, _ := os.ReadFile(calls)
		return strings.Count(string(data), "run")
	}
	first := "import \"std/test\";\nfunction main(): i32 { return 0; }\n"
	out, exit := CheckSourceVerdict(t, fake, first)
	if exit != 1 || string(out) != "args -check -\n"+first {
		t.Fatalf("verdict = %q exit %d, want the flags, the source on stdin and exit 1", out, exit)
	}
	CheckSourceVerdict(t, fake, first)
	if got := runs(); got != 1 {
		t.Fatalf("the same source ran the oracle %d times, want 1", got)
	}
	CheckSourceVerdict(t, fake, "import \"std/fs\";\nfunction main(): i32 { return 0; }\n")
	if got := runs(); got != 2 {
		t.Fatalf("a new source ran the oracle %d times, want 2", got)
	}
}

func TestInProcessVerdictKeysModeAndSource(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	src := filepath.Join(t.TempDir(), "main.fern")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runs := 0
	run := func() ([]byte, int) {
		runs++
		return []byte("refused"), 1
	}
	expect := func(want int, what string) {
		t.Helper()
		if runs != want {
			t.Fatalf("%s: run was called %d times, want %d", what, runs, want)
		}
	}
	write("function main(): i32 { return 0; }\n")
	out, exit := InProcessVerdict(t, "check", src, run)
	if exit != 1 || string(out) != "refused" {
		t.Fatalf("verdict = %q exit %d, want run's output and exit 1", out, exit)
	}
	out, exit = InProcessVerdict(t, "check", src, run)
	expect(1, "the same mode and source")
	if exit != 1 || string(out) != "refused" {
		t.Fatalf("cached verdict = %q exit %d, want run's output and exit 1", out, exit)
	}
	InProcessVerdict(t, "lint", src, run)
	expect(2, "a second mode")
	write("function main(): i32 { return 1; }\n")
	InProcessVerdict(t, "check", src, run)
	expect(3, "a changed source")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", "")
	InProcessVerdict(t, "check", src, run)
	InProcessVerdict(t, "check", src, run)
	expect(5, "no cache directory")
}

func TestInterpVerdictRunsExternalFernAgain(t *testing.T) {
	work := t.TempDir()
	fern := filepath.Join(work, "fern")
	interp := filepath.Join(work, "interp")
	src := filepath.Join(work, "main_test.fern")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(interp, "#!/bin/sh\nexec \"$FERN_BIN\"\n", 0o755)
	write(src, "function main(): i32 { return 0; }\n", 0o644)
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	t.Setenv("FERN_BIN", fern)
	for _, tc := range []struct {
		output string
		exit   int
	}{{"first", 0}, {"second", 7}} {
		write(fern, "#!/bin/sh\necho "+tc.output+"\nexit "+strconv.Itoa(tc.exit)+"\n", 0o755)
		out, exit := InterpVerdict(t, interp, src)
		if string(out) != tc.output+"\n" || exit != tc.exit {
			t.Fatalf("verdict = %q exit %d, want %q exit %d", out, exit, tc.output+"\n", tc.exit)
		}
	}
}

func TestInProcessSourceVerdictKeysModeAndSource(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	runs := 0
	run := func() ([]byte, int) {
		runs++
		return []byte("refused"), 1
	}
	expect := func(want int, what string) {
		t.Helper()
		if runs != want {
			t.Fatalf("%s: run was called %d times, want %d", what, runs, want)
		}
	}
	first := "import \"std/test\";\nfunction main(): i32 { return 0; }\n"
	out, exit := InProcessSourceVerdict(t, "check", first, run)
	if exit != 1 || string(out) != "refused" {
		t.Fatalf("verdict = %q exit %d, want run's output and exit 1", out, exit)
	}
	out, exit = InProcessSourceVerdict(t, "check", first, run)
	expect(1, "the same mode and source")
	if exit != 1 || string(out) != "refused" {
		t.Fatalf("cached verdict = %q exit %d, want run's output and exit 1", out, exit)
	}
	InProcessSourceVerdict(t, "lint", first, run)
	expect(2, "a second mode")
	InProcessSourceVerdict(t, "check", "import \"std/fs\";\nfunction main(): i32 { return 0; }\n", run)
	expect(3, "a changed source")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", "")
	InProcessSourceVerdict(t, "check", first, run)
	InProcessSourceVerdict(t, "check", first, run)
	expect(5, "no cache directory")
}

func TestInterpInputVerdictKeysTheInput(t *testing.T) {
	t.Setenv("FERN_BIN", "")
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	fake := filepath.Join(work, "fake-oracle")
	script := "#!/bin/sh\necho run >> " + calls + "\necho \"args $1\"\ncat\nexit 3\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(work, "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := func() int {
		data, _ := os.ReadFile(calls)
		return strings.Count(string(data), "run")
	}
	out, exit := InterpInputVerdict(t, fake, src, "first\n")
	if exit != 3 || string(out) != "args -interp\nfirst\n" {
		t.Fatalf("verdict = %q exit %d, want the flag, the input and exit 3", out, exit)
	}
	out, exit = InterpInputVerdict(t, fake, src, "first\n")
	if got := runs(); got != 1 || exit != 3 || string(out) != "args -interp\nfirst\n" {
		t.Fatalf("the same input ran the oracle %d times and gave %q exit %d, want 1 run and the kept verdict", got, out, exit)
	}
	InterpInputVerdict(t, fake, src, "second\n")
	if got := runs(); got != 2 {
		t.Fatalf("a new input ran the oracle %d times, want 2", got)
	}
	InterpInputVerdict(t, fake, src, "")
	InterpVerdict(t, fake, src)
	if got := runs(); got != 3 {
		t.Fatalf("an empty input and no input ran the oracle %d times, want 3: they are the same verdict", got)
	}
}
