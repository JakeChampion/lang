package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

type selfHostFinding struct {
	Test  string `json:"test"`
	Seed  uint64 `json:"seed"`
	Class string `json:"class"`
	Pass  string `json:"pass"`
}

var findingsMu sync.Mutex

func recordFinding(t *testing.T, f selfHostFinding) {
	t.Helper()
	path := os.Getenv("FERN_FINDINGS")
	if path == "" {
		return
	}
	line, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("finding: %v", err)
	}
	findingsMu.Lock()
	defer findingsMu.Unlock()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = fh.Write(append(line, '\n'))
		if cerr := fh.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		t.Errorf("FERN_FINDINGS: %v", err)
	}
}

func failureClass(format string) string {
	line, _, _ := strings.Cut(format, "\n")
	if i := strings.IndexByte(line, '%'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimRight(line, " =(—-:,")
}

func refusalClass(gap string) string {
	switch {
	case strings.Contains(gap, " emits unbalanced reference counting"):
		return "refused: unbalanced reference counting"
	case strings.Contains(gap, " lowered to malformed IR"):
		return "refused: malformed IR"
	}
	for _, line := range strings.Split(gap, "\n") {
		rest, ok := strings.CutPrefix(line, "FERN_SEM_IR: ")
		if !ok || strings.HasPrefix(rest, "the typed lowering refused") {
			continue
		}
		if _, why, ok := strings.Cut(rest, ": "); ok {
			rest = why
		}
		return "refused: " + rest
	}
	return "refused"
}

func legName(t *testing.T) string {
	leg, _, _ := strings.Cut(t.Name(), "/")
	return leg
}

func TestFailureClass(t *testing.T) {
	for format, want := range map[string]string{
		"self-host exit = %d, interp = %d%s\nstdout: %q":                         "self-host exit",
		"the self-host-compiled binary HUNG — killed at %s\nstdout so far: %q":   "the self-host-compiled binary HUNG — killed at",
		"the sanitizer reported on the self-host-compiled binary:\n%s\nsrc:\n%s": "the sanitizer reported on the self-host-compiled binary",
		"the self-host compiler did not finish in %v:\nsrc:\n%s":                 "the self-host compiler did not finish in",
	} {
		if got := failureClass(format); got != want {
			t.Errorf("failureClass(%q) = %q, want %q", format, got, want)
		}
	}
	gap := "exit status 3\nFERN_SEM_IR: main: copy or phi type\nFERN_SEM_IR: the typed lowering refused 1 declarations and instances; the FERN_SEM_IR lines above name each\n"
	if got := refusalClass(gap); got != "refused: copy or phi type" || !compilerRefused(gap) {
		t.Errorf("refusalClass = %q, compilerRefused = %v", got, compilerRefused(gap))
	}
	for gap, want := range map[string]string{
		"exit status 5\nFERN_RC_CERTIFY: gen_f0 emits unbalanced reference counting (#11908):\n    v3 released twice": "refused: unbalanced reference counting",
		"exit status 4\nFERN_IR_VERIFY: main lowered to malformed IR (#6639):\n    op 7 pops past the stack":          "refused: malformed IR",
	} {
		if got := refusalClass(gap); got != want || !compilerRefused(gap) {
			t.Errorf("refusalClass(%q) = %q, compilerRefused = %v; want %q and a refusal", gap, got, compilerRefused(gap), want)
		}
	}
	if compilerRefused("exit status 3\nFERN_STRICT_IR: main: an op the subset does not lower") {
		t.Error("a coverage gap read as a compiler refusal")
	}
}

func TestRecordFindingAppendsJSONLines(t *testing.T) {
	path := t.TempDir() + "/findings.jsonl"
	t.Setenv("FERN_FINDINGS", path)
	recordFinding(t, selfHostFinding{Test: "TestDifferential_SelfHostX86_64", Seed: 3, Class: "refused: copy or phi type", Pass: "inline"})
	recordFinding(t, selfHostFinding{Test: "TestDifferential_SelfHostWasm", Seed: 9, Class: "self-host wasm exit", Pass: "none"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), data)
	}
	var f selfHostFinding
	if err := json.Unmarshal([]byte(lines[0]), &f); err != nil || f.Seed != 3 || f.Pass != "inline" {
		t.Fatalf("first line %q decodes to %+v, %v", lines[0], f, err)
	}
}
