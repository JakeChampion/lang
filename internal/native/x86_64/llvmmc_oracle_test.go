package x86_64_test

// llvm-mc second oracle (issue #7895): cross-checks the assembler against
// LLVM's integrated assembler using the SAME form inventory the gas fuzz
// lane defines (fuzz_gas_test.go) — same seed, same units. Skips cleanly
// when llvm-mc is not installed; CI runs it in one Linux job.
//
// A gas-vs-llvm-mc disagreement is a FINDING: the failure prints our bytes,
// llvm-mc's, and (when binutils is present) gas's, and is not auto-resolved.
//
// Forms compared by decoded text rather than bytes:
//   - the compareDecode forms of the gas lane (xchg-with-accumulator
//     shortenings, which llvm-mc applies like gas);
//   - string_ops, because llvm-mc orders a rep prefix BEFORE the 0x66
//     operand-size prefix ([f3 66 a5] for `rep movsw`) where GNU as — and
//     this assembler — put 0x66 first. Both encodings are legal and decode
//     identically; byte-matching both oracles at once is impossible there.
//   - xchg, because for xchg reg, reg llvm-mc puts the opposite operand in
//     ModRM.reg from gas (visible only when the REX extension bits differ,
//     e.g. `xchg sp, r12w`: gas 66 44 87 e4, llvm-mc 66 41 87 e4) — the
//     instruction is symmetric, so both decode to the same operation.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// llvmDecodeForms are byte-compared against gas but only decode-compared
// against llvm-mc (see the file comment).
var llvmDecodeForms = map[string]bool{"string_ops": true, "xchg": true}

func findLLVMMC(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"llvm-mc", "llvm-mc-19", "llvm-mc-18", "llvm-mc-17"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("llvm-mc not on PATH")
	return ""
}

var llvmEncodingLine = regexp.MustCompile(`encoding: \[([^\]]+)\]`)

// llvmMCEncode assembles src with llvm-mc --show-encoding and returns the
// encoded bytes per statement. A non-hex entry (an unresolved-fixup 'A')
// or a statement-count mismatch is an error: the caller's units must each
// be one label-free instruction.
func llvmMCEncode(t *testing.T, mc string, args []string, src string, nUnits int) [][]byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(mc, append(args, "--show-encoding", sPath)...).CombinedOutput()
	if err != nil {
		t.Fatalf("llvm-mc: %v\n%s", err, out)
	}
	var encs [][]byte
	for _, line := range strings.Split(string(out), "\n") {
		m := llvmEncodingLine.FindStringSubmatch(line)
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

// TestEncodingsAgainstLLVMMC is the x86-64 llvm-mc lane. Label-bearing
// (multi) forms are skipped: --show-encoding leaves their branch bytes as
// unresolved fixups.
func TestEncodingsAgainstLLVMMC(t *testing.T) {
	mc := findLLVMMC(t)
	args := []string{"--arch=x86-64", "-x86-asm-syntax=intel"}
	seed := fuzzSeed(t)
	n := fuzzCaseCount()
	for _, f := range x86Forms() {
		f := f
		if f.multi {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			units := formUnits(f, formRand(seed, f.name), n)
			src := strings.Join(units, "")
			got, err := ourBytes(t, src)
			if err != nil {
				t.Fatalf("AssembleProgram: %v", err)
			}
			encs := llvmMCEncode(t, mc, args, ".intel_syntax noprefix\n"+src, len(units))
			want := bytes.Join(encs, nil)
			if f.mode == compareDecode || llvmDecodeForms[f.name] {
				objdump := findObjdump(t)
				gotI := objdumpX86(t, objdump, got)
				wantI := objdumpX86(t, objdump, want)
				if len(gotI) != len(wantI) {
					t.Fatalf("decode counts differ: ours %d insns, llvm-mc %d", len(gotI), len(wantI))
				}
				for i := range gotI {
					if gotI[i] != wantI[i] {
						t.Errorf("decode differs at insn %d:\n ours    %s\n llvm-mc %s\n(seed %d, form %s)",
							i, gotI[i], wantI[i], seed, f.name)
						return
					}
				}
				return
			}
			if !bytes.Equal(got, want) {
				minimizeLLVMMismatch(t, f, units, encs, seed)
			}
		})
	}
}

// minimizeLLVMMismatch names the first diverging unit, including gas's
// bytes when binutils is available so an oracle-vs-oracle disagreement is
// visible in the failure.
func minimizeLLVMMismatch(t *testing.T, f asmForm, units []string, encs [][]byte, seed int64) {
	t.Helper()
	var as, objcopy string
	if a, err := exec.LookPath("as"); err == nil {
		if o, err := exec.LookPath("objcopy"); err == nil {
			as, objcopy = a, o
		}
	}
	for i, u := range units {
		got, err := ourBytes(t, u)
		if err != nil {
			t.Fatalf("unit stopped assembling alone:\n%s error: %v", u, err)
		}
		if !bytes.Equal(got, encs[i]) {
			gasBytes := "(binutils not available)"
			if as != "" {
				gasBytes = fmt.Sprintf("% x", gnuAsX86Text(t, as, objcopy, u))
			}
			t.Fatalf("encoding differs from llvm-mc (seed %d, form %s) — pin as:\n"+
				"source:\n%s ours:    % x\n llvm-mc: % x\n gas:     %s", seed, f.name, u, got, encs[i], gasBytes)
		}
	}
	t.Fatalf("batch bytes differ but every unit matches (form %s, seed %d)", f.name, seed)
}
