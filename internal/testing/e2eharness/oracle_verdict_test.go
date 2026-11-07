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

	t.Setenv("FERN_SELFHOST_BUILD_CACHE", "")
	check(5)
	check(6)
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
