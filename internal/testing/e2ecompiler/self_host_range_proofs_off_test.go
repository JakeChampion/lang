package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const noRangeProofs = "FERN_DEBUG_NO_RANGE_PROOFS=1"

const rangeProofLoop = `@noinline function sum(xs: i32[], lo: i32, hi: i32, c: i32): i32 {
    let s: i32 = 0;
    for i in lo..hi { s = s + xs[i + c]; }
    return s;
}
`

const rangeProofWord = `@noinline function le32(bs: u8[], at: i32): u32 { return bs[at] as u32 | bs[at + 1] as u32 << 8 | bs[at + 2] as u32 << 16 | bs[at + 3] as u32 << 24; }
`

// Each program that aborts has a twin before it that does not.
var rangeProofPrograms = []struct {
	name   string
	src    string
	aborts bool
}{
	{"loop_offset_in_range", rangeProofLoop + `function main(): i32 { print(sum([1, 2, 3, 4], 0, 3, 1).to_string()); print(sum([1, 2, 3, 4], 3, 0, 9).to_string()); return 0; }`, false},
	{"loop_offset_past_length", rangeProofLoop + `function main(): i32 { print(sum([1, 2, 3, 4], 0, 3, 1).to_string()); print(sum([1, 2, 3, 4], 0, 3, 2).to_string()); return 0; }`, true},
	{"loop_offset_wraps", rangeProofLoop + `function main(): i32 { print(sum([1, 2, 3, 4], 0, 3, 1).to_string()); print(sum([1, 2, 3, 4], 1, 3, 2147483647).to_string()); return 0; }`, true},
	{"loop_from_below_zero", rangeProofLoop + `function main(): i32 { print(sum([1, 2, 3, 4], 0, 3, 1).to_string()); print(sum([1, 2, 3, 4], 0 - 1, 2, 0).to_string()); return 0; }`, true},
	{"loop_to_the_end_of_i32", rangeProofLoop + `function main(): i32 { print(sum([1, 2, 3, 4], 0, 3, 1).to_string()); print(sum([1, 2, 3, 4], 2, 2147483647, 0).to_string()); return 0; }`, true},
	{"word_at_the_end", rangeProofWord + `function main(): i32 { let bs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8, 6u8]; print(le32(bs, 0).to_string()); print(le32(bs, 2).to_string()); return 0; }`, false},
	{"word_past_the_end", rangeProofWord + `function main(): i32 { let bs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8, 6u8]; print(le32(bs, 0).to_string()); print(le32(bs, 3).to_string()); return 0; }`, true},
	{"word_before_the_start", rangeProofWord + `function main(): i32 { let bs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8, 6u8]; print(le32(bs, 0).to_string()); print(le32(bs, 0 - 1).to_string()); return 0; }`, true},
	{"word_offset_wraps", rangeProofWord + `function main(): i32 { let bs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8, 6u8]; print(le32(bs, 0).to_string()); print(le32(bs, 2147483645).to_string()); return 0; }`, true},
	{"quotient_of_guarded_value", `@noinline function q(x: i32): i32 { if (x < 0) { return 0 - 1; } return x / 4 + x % 8; }
function main(): i32 { print(q(2147483647).to_string()); print(q(9).to_string()); print(q(0 - 9).to_string()); return 0; }`, false},
	{"quotient_of_wrapped_sum", `@noinline function q(x: i32): i32 { if (x < 0) { return 0 - 1; } let y: i32 = x + 1; return y / 4 + y % 8; }
function main(): i32 { print(q(2147483647).to_string()); print(q(7).to_string()); return 0; }`, false},
	{"quotient_of_length_difference", `@noinline function q(xs: i32[], k: i32): i32 { return (xs.len() - k) / 2 + (xs.len() - k) % 4; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; print(q(xs, 1).to_string()); print(q(xs, 6).to_string()); print(q(xs, 0 - 2147483647).to_string()); return 0; }`, false},
	{"quotient_by_variable", `@noinline function q(x: i32, d: i32): i32 { if (x < 0) { return 0 - 1; } return x / d + x % d; }
function main(): i32 { print(q(9, 4).to_string()); print(q(9, 0).to_string()); print(q(2147483647, 0 - 1).to_string()); return 0; }`, false},
}

// proofRun is the stdout, the exit code and the cause line of an abort, or ""
// when the program ends without one.
type proofRun struct {
	stdout string
	cause  string
	code   int
}

func runProofProgram(t *testing.T, cmd *exec.Cmd) proofRun {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%v did not exit: %s", cmd.Args, errb.String())
	}
	r := proofRun{stdout: out.String(), code: cmd.ProcessState.ExitCode()}
	for _, line := range strings.Split(errb.String(), "\n") {
		if strings.HasPrefix(line, "fern: ") {
			r.cause = line
			break
		}
	}
	return r
}

// FERN_DEBUG_NO_RANGE_PROOFS=1 keeps each check that a proof of a range
// removes. A program then does the same with and without the proofs: the
// same lines, the same exit code, and the same abort or none.
func TestSelfHostRangeProofsOff(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	write := func(t *testing.T, program string) string {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(program+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return src
	}
	for _, tw := range guardBoundsTwins {
		t.Run("restores_checks/"+tw.name, func(t *testing.T) {
			t.Parallel()
			src := write(t, tw.proven)
			traps := func(env ...string) int {
				asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux", env...))
				if err != nil {
					t.Fatal(err)
				}
				return bytes.Count(asm, []byte("__fern_oob_abort")) + bytes.Count(asm, []byte("__fern_str_oob_abort"))
			}
			if with, without := traps(), traps(noRangeProofs); without <= with {
				t.Errorf("%d bounds traps without the proofs against %d with them, want more", without, with)
			}
		})
	}
	type program struct {
		name, src string
		aborts    bool
	}
	var programs []program
	for _, c := range guardBoundsCases {
		programs = append(programs, program{c.name, c.src, false})
	}
	for _, c := range guardBoundsUnsafe {
		programs = append(programs, program{c.name, c.src, true})
	}
	for _, c := range rangeProofPrograms {
		programs = append(programs, program{c.name, c.src, c.aborts})
	}
	for _, p := range programs {
		t.Run("same_result/"+p.name, func(t *testing.T) {
			t.Parallel()
			src := write(t, p.src)
			engines := map[string]func(env ...string) *exec.Cmd{
				"x86-64-linux": func(env ...string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.x86Binary(t, src, env...)) },
				"arm64-linux":  func(env ...string) *exec.Cmd { return runArm64Bin(qemu, cli.arm64Binary(t, src, env...)) },
			}
			for target, engine := range engines {
				with, without := runProofProgram(t, engine()), runProofProgram(t, engine(noRangeProofs))
				if with != without {
					t.Errorf("%s with the proofs: stdout %q, exit %d, cause %q\nwithout them: stdout %q, exit %d, cause %q", target, with.stdout, with.code, with.cause, without.stdout, without.code, without.cause)
				}
				if aborted := without.cause != ""; aborted != p.aborts {
					t.Errorf("%s without the proofs: exit %d, cause %q, want abort %v", target, without.code, without.cause, p.aborts)
				}
			}
		})
	}
}
