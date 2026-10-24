package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostIRKindRegistry(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ir_kind_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ir_kind_run.fern", "ir_kind_run")

	const want = "kind_count=188\n" +
		"bijection_ok=162\n" +
		"bijection_failures=0\n" +
		"retired=26\n" +
		"ext_ok=257\n" +
		"ext_failures=0\n" +
		"neg_ok=15\n" +
		"neg_failures=0\n" +
		"unknown_id=0\n" +
		"id0=invalid\n" +
		"oob=invalid\n" +
		"const_i32=1\n" +
		"add=21\n" +
		"call_direct=149\n" +
		"return=160\n" +
		"is_const const_i32=1 const_str=1 add=0\n" +
		"is_term return=1 br=1 exit=1 brif=0\n" +
		"is_fold add=1 div_s=1 ge_s=1 fadd=0\n" +
		"is_commute add=1 xor=1 sub=0 shl=0\n" +
		"tag_consistency ok=228 bad=0\n"

	table, err := os.ReadFile(filepath.Join("testdata", "ir-kind-ids.txt"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_kind_run did not exit normally")
	}
	if got := string(out); got != want+string(table) {
		t.Errorf("kind registry report mismatch:\n--- got ---\n%s\n--- want ---\n%s%s", got, want, table)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ir_kind_run exit code = %d, want 0 (total failures across the four sweeps)", code)
	}
}

func TestSelfHostIROpConstructorTags(t *testing.T) {
	table, err := os.ReadFile(filepath.Join("testdata", "ir-kind-ids.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(table)), "\n") {
		name, id, _ := strings.Cut(line, "=")
		ids[name] = id
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "ir.fern"))
	if err != nil {
		t.Fatal(err)
	}
	ctor := regexp.MustCompile(`(?m)^(?:pub )?function op_(\w+)\([^)]*\): Op \{\s*return (?:Op \{\s*\.\.\.)?op0\((\d+)\)`)
	matches := ctor.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 200 {
		t.Fatalf("found %d op0 constructors in ir.fern, want at least 200 — the pattern no longer matches", len(matches))
	}
	for _, m := range matches {
		kind := m[1]
		want, ok := ids[kind]
		if !ok {
			t.Errorf("op_%s: no kind %q in ir-kind-ids.txt", m[1], kind)
			continue
		}
		if m[2] != want {
			t.Errorf("op_%s builds tag %s, but %s is %s", m[1], m[2], kind, want)
		}
	}
	if n := len(regexp.MustCompile(`\bop0\(`).FindAllString(string(src), -1)); n != len(matches)+1 {
		t.Errorf("ir.fern has %d op0 uses but %d matched constructors (+ the definition): an unchecked call site", n, len(matches))
	}
}

func TestSelfHostIRKindConstants(t *testing.T) {
	table, err := os.ReadFile(filepath.Join("testdata", "ir-kind-ids.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(table)), "\n") {
		name, id, _ := strings.Cut(line, "=")
		ids[name] = id
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "ir.fern"))
	if err != nil {
		t.Fatal(err)
	}
	konst := regexp.MustCompile(`(?m)^pub const K_(\w+): i32 = (\d+);`)
	matches := konst.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 60 {
		t.Fatalf("found %d K_ constants in ir.fern, want at least 60 — the pattern no longer matches", len(matches))
	}
	for _, m := range matches {
		kind := strings.ToLower(m[1])
		want, ok := ids[kind]
		if !ok {
			t.Errorf("K_%s: no kind %q in ir-kind-ids.txt", m[1], kind)
			continue
		}
		if m[2] != want {
			t.Errorf("K_%s is %s, but %s is %s", m[1], m[2], kind, want)
		}
	}
}
