package arm64_test

// llvm-mc second oracle (issue #7895) for arm64: cross-checks the assembler
// against LLVM's integrated assembler using the SAME form inventory as the
// gas fuzz lane (fuzz_gas_test.go) — same seed, same units. Skips cleanly
// when llvm-mc is not installed; CI runs it in one Linux job.
//
// A gas-vs-llvm-mc disagreement is a FINDING: the failure prints our bytes,
// llvm-mc's, and (when the cross binutils are present) gas's, and is not
// auto-resolved.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

func a64FindLLVMMC(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"llvm-mc", "llvm-mc-19", "llvm-mc-18", "llvm-mc-17"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("llvm-mc not on PATH")
	return ""
}

var a64EncodingLine = regexp.MustCompile(`encoding: \[([^\]]+)\]`)

func a64LLVMMCEncode(t *testing.T, mc, src string, nUnits int) [][]byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(mc, "--arch=aarch64", "--show-encoding", sPath).CombinedOutput()
	if err != nil {
		t.Fatalf("llvm-mc: %v\n%s", err, out)
	}
	var encs [][]byte
	for _, line := range strings.Split(string(out), "\n") {
		m := a64EncodingLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var b []byte
		for _, part := range strings.Split(m[1], ",") {
			var v byte
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "0x%02x", &v); err != nil {
				t.Fatalf("llvm-mc emitted a non-byte encoding entry %q in %q", part, line)
			}
			b = append(b, v)
		}
		encs = append(encs, b)
	}
	if len(encs) != nUnits {
		t.Fatalf("llvm-mc emitted %d encodings for %d instructions:\n%s", len(encs), nUnits, out)
	}
	return encs
}

// TestEncodingsAgainstLLVMMC is the arm64 llvm-mc lane. Label-bearing
// (multi) forms are skipped: --show-encoding leaves their branch words as
// unresolved fixups.
func TestEncodingsAgainstLLVMMC(t *testing.T) {
	mc := a64FindLLVMMC(t)
	seed := a64FuzzSeed(t)
	n := a64FuzzCases()
	for _, f := range a64Forms() {
		f := f
		if f.multi {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			units := a64FormUnits(f, a64FormRand(seed, f.name), n)
			src := strings.Join(units, "")
			got, err := arm64.Assemble(src)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			encs := a64LLVMMCEncode(t, mc, src, len(units))
			want := bytes.Join(encs, nil)
			if !bytes.Equal(got, want) {
				a64MinimizeLLVM(t, f, units, encs, seed)
			}
		})
	}
}

func a64MinimizeLLVM(t *testing.T, f a64Form, units []string, encs [][]byte, seed int64) {
	t.Helper()
	var as, objcopy string
	if a, err := exec.LookPath("aarch64-linux-gnu-as"); err == nil {
		if o, err := exec.LookPath("aarch64-linux-gnu-objcopy"); err == nil {
			as, objcopy = a, o
		}
	}
	for i, u := range units {
		got, err := arm64.Assemble(u)
		if err != nil {
			t.Fatalf("unit stopped assembling alone:\n%s error: %v", u, err)
		}
		if !bytes.Equal(got, encs[i]) {
			gasBytes := "(cross binutils not available)"
			if as != "" {
				gasBytes = fmt.Sprintf("% x", gnuAsText(t, as, objcopy, u))
			}
			t.Fatalf("encoding differs from llvm-mc (seed %d, form %s) — pin as:\n"+
				"source:\n%s ours:    % x\n llvm-mc: % x\n gas:     %s", seed, f.name, u, got, encs[i], gasBytes)
		}
	}
	t.Fatalf("batch bytes differ but every unit matches (form %s, seed %d)", f.name, seed)
}
