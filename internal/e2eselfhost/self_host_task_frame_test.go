package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestSelfHostTaskFrame pins what a parked frame keeps
// (docs/NET-P3-SUSPENSION-PLAN.md §3.5): views and a shared mutated capture
// read after a park answer what the plain run answers, on x86-64 and arm64
// through the self-host CLI, and a park saves only the locals live across it.
func TestSelfHostTaskFrame(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("saves", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(e2eharness.TaskFrameProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, cli.stdlib)
		cmd.Env = append(os.Environ(), "FERN_SUSPEND_DUMP=rounds")
		msg, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("self-host CLI: %v\n%s", err, msg)
		}
		checkRoundsSaves(t, string(msg))
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(e2eharness.TaskFrameProgram), 0o644); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
				if len(cli.runner) == 0 {
					cmd = exec.Command(bin)
				} else {
					cmd = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
				}
			case "arm64-linux":
				armgcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, "FERN_STRICT_IR=1"))
				if err != nil {
					t.Fatal(err)
				}
				cmd = runArm64Bin(qemu, buildBinArm64(t, armgcc, filepath.Dir(src), "prog", string(asm)))
			}
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
			}
			if out.String() != e2eharness.TaskFrameWant {
				t.Fatalf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out.String(), e2eharness.TaskFrameWant, errb.String())
			}
		})
	}
}

// checkRoundsSaves reads the suspend pass's dump of `rounds`: the slot the
// first array literal, `gone`, is stored to is dead at the park and must not be saved, and
// the park saves well under the body's locals.
func checkRoundsSaves(t *testing.T, dump string) {
	t.Helper()
	before, after, ok := strings.Cut(dump, "== rounds after")
	if !ok {
		t.Fatalf("no dump of rounds after the suspend pass:\n%s", dump)
	}
	array, locals := -1, 0
	lines := strings.Split(before, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "== rounds before n_locals=") {
			locals, _ = strconv.Atoi(strings.TrimPrefix(line, "== rounds before n_locals="))
		}
		if f := strings.Fields(line); array < 0 && len(f) >= 2 && f[1] == "arr_make" && i+1 < len(lines) {
			if g := strings.Fields(lines[i+1]); len(g) >= 3 && g[1] == "store_local" {
				array, _ = strconv.Atoi(g[2])
			}
		}
	}
	if array < 0 || locals == 0 {
		t.Fatalf("no array slot or local count in the dump of rounds:\n%s", before)
	}
	var saved []int
	lines = strings.Split(after, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "__fern_task_push_") || i == 0 {
			continue
		}
		if f := strings.Fields(lines[i-1]); len(f) >= 3 && f[1] == "load_local" {
			n, _ := strconv.Atoi(f[2])
			saved = append(saved, n)
		}
	}
	for _, s := range saved {
		if s == array {
			t.Fatalf("the park saves slot %d, the array dead by then (saved %v)", s, saved)
		}
	}
	if len(saved) == 0 || len(saved)*4 > locals {
		t.Fatalf("the park saves %d of the body's %d locals (%v), want the live few", len(saved), locals, saved)
	}
}
