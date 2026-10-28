package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const semUnrollProgram = `@inline
function pick(v: f64x2, i: i32): f64 {
    if (i == 0) { return __f64x2_lane(v, 0); }
    return __f64x2_lane(v, 1);
}
@noinline function summed(v: f64x2): f64 {
    let s: f64 = 0.0;
    let i: i32 = 0;
    while (i < 2) { s = s + pick(v, i); i = i + 1; }
    return s;
}
@noinline function kept17(): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 17) { s = s + i; i = i + 1; }
    return s;
}
@noinline function unknown(n: i32): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < n) { s = s + i; i = i + 1; }
    return s;
}
function main(): i32 {
    print(summed(__f64x2_of(1.5, 2.25)));
    print(kept17() + unknown(4));
    return 0;
}
`

// semDumpBody is the printed graph of one function from a FERN_SEM_DUMP run.
func semDumpBody(t *testing.T, dump, name string) string {
	t.Helper()
	start := strings.Index(dump, "func "+name+" {\n")
	if start < 0 {
		t.Fatalf("no graph for %s in the dump:\n%s", name, dump)
	}
	end := strings.Index(dump[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("unterminated graph for %s", name)
	}
	return dump[start : start+end]
}

func TestSelfHostSemanticUnrollCountedLoops(t *testing.T) {
	c := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(semUnrollProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "main")
	cmd := runX86_64Bin(c.runner, c.bin, "-target", "x86-64-linux", "-o", bin, src, c.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SEM_DUMP=*")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	dump := string(out)

	summed := semDumpBody(t, dump, "summed")
	if strings.Contains(summed, "brif") {
		t.Errorf("summed keeps a loop or a lane branch:\n%s", summed)
	}
	if n := strings.Count(summed, "call __f64x2_lane"); n != 2 {
		t.Errorf("summed reads %d lanes, want 2:\n%s", n, summed)
	}
	for _, name := range []string{"kept17", "unknown"} {
		if body := semDumpBody(t, dump, name); !strings.Contains(body, "brif") || !strings.Contains(body, "phi") {
			t.Errorf("%s lost its loop:\n%s", name, body)
		}
	}

	got, err := runX86_64Bin(c.runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if string(got) != "3.75\n142\n" {
		t.Errorf("output %q, want %q", got, "3.75\n142\n")
	}
}
