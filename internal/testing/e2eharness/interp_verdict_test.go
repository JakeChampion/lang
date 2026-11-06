package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterpVerdictCachesByInputs(t *testing.T) {
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	fake := filepath.Join(work, "fake-interp")
	script := "#!/bin/sh\necho run >> " + calls + "\necho \"verdict for $2\"\nexit 3\n"
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

	t.Setenv("FERN_SELFHOST_BUILD_CACHE", "")
	check(4)
	check(5)
}
