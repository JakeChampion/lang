package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// FERN_SSA_DENY keeps the allocator off the registers of its mask. The
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
	denied, err := os.ReadFile(cli.emit(t, src, "x86-64-linux", "FERN_SSA_DENY=16383"))
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
			if stderr, code := cli.exitOf(t, loadCompareSpilledProg, target, "FERN_SSA_DENY="+mask); code != 0 {
				t.Errorf("%s FERN_SSA_DENY=%s: exit %d, want 0\n%s", target, mask, code, stderr)
			}
		}
	}
}
