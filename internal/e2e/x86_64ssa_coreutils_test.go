package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The three coreutils that -backend ssa could not build for x86-64 (#9559),
// for two unrelated reasons: uniq and sort hit a duplicate `.Lssa_mm_vec`,
// because __ssa_mismatch and __fern_mismatch shared a label prefix and both
// are needed once a program compares strings; wc had no emitter for
// fn___method_Reader_stat.
//
// They are the measurement corpus the retention work reads, so "it builds" is
// not enough — a binary that links and prints the wrong thing is worse than
// one that fails to link. Each is run against the same input as the
// stack-machine build and the outputs must agree, which makes the flat
// backend the oracle rather than a hardcoded expectation.
func TestX86_64SSABuildsAndRunsTheCoreutils(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	// Repeats, an already-sorted run and a trailing group, so uniq and sort
	// each have something to do.
	input := "banana\napple\napple\ncherry\napple\nbanana\nbanana\ndate\n"
	inPath := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(inPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	for _, util := range []string{"uniq", "sort", "wc"} {
		t.Run(util, func(t *testing.T) {
			src := filepath.Join("..", "..", "coreutils", util+".fern")
			build := func(backend string) string {
				out := filepath.Join(dir, util+"."+backend)
				cmd := exec.Command(bin, "-target", "x86-64-linux", "-backend", backend, "-o", out, src)
				cmd.Env = e2eharness.ChildEnv()
				if outB, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s -backend %s build failed: %v\n%s", util, backend, err, outB)
				}
				return out
			}
			run := func(binPath string, args ...string) string {
				f, err := os.Open(inPath)
				if err != nil {
					t.Fatalf("open input: %v", err)
				}
				defer f.Close()
				cmd := runX86Bin(qemu, binPath, args...)
				cmd.Env = e2eharness.ChildEnv()
				cmd.Stdin = f
				var out, errBuf bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &errBuf
				if err := cmd.Run(); err != nil {
					if _, ok := err.(*exec.ExitError); !ok {
						t.Fatalf("run %s: %v\n%s", binPath, err, errBuf.String())
					}
				}
				return out.String()
			}

			ssaBin, flatBin := build("ssa"), build("flat")
			// wc reaches Reader.stat only when given a path; stdin exercises
			// the counting path instead, so both are compared.
			cases := [][]string{nil}
			if util == "wc" {
				cases = append(cases, []string{inPath})
			}
			for _, args := range cases {
				got, want := run(ssaBin, args...), run(flatBin, args...)
				if got != want {
					label := "stdin"
					if len(args) > 0 {
						label = "file argument"
					}
					t.Errorf("%s (%s): -backend ssa and -backend flat disagree.\nssa:  %q\nflat: %q",
						util, label, strings.TrimRight(got, "\n"), strings.TrimRight(want, "\n"))
				}
			}
		})
	}
}
