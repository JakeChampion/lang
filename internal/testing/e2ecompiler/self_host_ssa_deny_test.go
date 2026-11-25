package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// FERN_DEBUG_SSA_DENY keeps the allocator off the registers of its mask. The
// program of TestSelfHostCompareFromMemoryKeepsItsBase has more live values
// than registers, so each mask gives a different home to its values, and
// each build must compute the same result.
func TestSelfHostAllocatorDenyMask(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(loadCompareSpilledProg), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := regexp.MustCompile(`%rbx|%r12|%r13|%r14|%r15`)
	open, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	if !saved.MatchString(tailCallBody(t, string(open), "over")) {
		t.Fatal("over uses no saved register with no mask, so the mask test proves nothing")
	}
	// 16383 is each integer register of x86-64.
	denied, err := os.ReadFile(cli.emit(t, src, "x86-64-linux", "FERN_SSA_DENY=0", "FERN_DEBUG_SSA_DENY=16383"))
	if err != nil {
		t.Fatal(err)
	}
	if body := tailCallBody(t, string(denied), "over"); saved.MatchString(body) {
		t.Errorf("over uses a saved register with each integer register denied:\n%s", body)
	}
	// Each integer register denied, then one register left: rcx, rdx, r11,
	// rax. The last mask denies each float register also.
	masks := map[string][]string{
		"x86-64-linux": {"16383", "16319", "16255", "16127", "16382", "134217727"},
		"arm64-linux":  {"8388607", "8388606", "140737488355327"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, mask := range masks[target] {
			if stderr, code := cli.exitOf(t, loadCompareSpilledProg, target, "FERN_DEBUG_SSA_DENY="+mask); code != 0 {
				t.Errorf("%s FERN_DEBUG_SSA_DENY=%s: exit %d, want 0\n%s", target, mask, code, stderr)
			}
		}
	}
}

const spilledConstantProg = `function mix(n: i32, seed: i64): i64 {
  let h = seed;
  let i = 0;
  while (i < n) {
    h = (h * 1000003i64 + i as i64) ^ 77777i64;
    i += 1;
  }
  return h;
}

function main(): i32 {
  if (mix(1000, 7i64) != -1413053815659120261i64) {
    return 1;
  }
  return 0;
}
`

// With each integer register denied, the constants of the loop are in frame
// slots. A read of such a constant on arm64 writes the value again, so the
// literal appears at its definition and at its read, and no read loads it.
func TestSelfHostSpilledConstantIsRewritten(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(spilledConstantProg), 0o644); err != nil {
		t.Fatal(err)
	}
	const deny = "FERN_DEBUG_SSA_DENY=8388607"
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", deny))
	if err != nil {
		t.Fatal(err)
	}
	if n := regexp.MustCompile(`=77777\b`).FindAll(asm, -1); len(n) != 2 {
		t.Errorf("arm64 writes the spilled constant 77777 %d times, want 2 (its definition and its read)", len(n))
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		if stderr, code := cli.exitOf(t, spilledConstantProg, target, deny); code != 0 {
			t.Errorf("%s %s: exit %d, want 0\n%s", target, deny, code, stderr)
		}
	}
}
