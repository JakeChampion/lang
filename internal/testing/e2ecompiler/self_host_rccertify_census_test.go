package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The runtime is compiled as a unit with a gate of its own. Its census lines
// must reach the report like those of the program.
func TestSelfHostRcCertifyCensusCoversTheRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(sanSelfHostCleanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := compileCapturing(t, cli, src, "FERN_RC_CERTIFY=report", "FERN_RC_CERTIFY_MEMO=0")
	if code != 0 {
		t.Fatalf("report compile: exit %d\n%s", code, out)
	}
	if !regexp.MustCompile(`(?m)^FERN_RC_CERTIFY: census __fern_\S+ tracked=`).MatchString(out) {
		t.Fatalf("no census line for a runtime function:\n%s", out)
	}
}

var mutantLine = regexp.MustCompile(`^FERN_RC_CERTIFY: mutants \S+ refuted=(\d+) weakened=(\d+) missed=(\d+)$`)

// FERN_RC_CERTIFY=mutate certifies up to three mutants of each function that
// passes. main holds a counted box, so each of its mutants is an imbalance the
// certifier must refute. The compile still succeeds: no mutant is emitted.
func TestSelfHostRcCertifyMutants(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(sanSelfHostCleanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := compileCapturing(t, cli, src, "FERN_RC_CERTIFY=mutate", "FERN_RC_CERTIFY_MEMO=0")
	if code != 0 {
		t.Fatalf("mutate compile: exit %d\n%s", code, out)
	}
	var mainLine []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "FERN_RC_CERTIFY: mutants ") {
			continue
		}
		m := mutantLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("malformed mutant line %q", line)
		}
		if strings.HasPrefix(line, "FERN_RC_CERTIFY: mutants main ") {
			mainLine = m
		}
	}
	if mainLine == nil {
		t.Fatalf("no mutants of main:\n%s", out)
	}
	refuted, _ := strconv.Atoi(mainLine[1])
	if refuted == 0 || mainLine[2] != "0" || mainLine[3] != "0" {
		t.Fatalf("mutants of main: %q, want each one refuted", mainLine[0])
	}
}
