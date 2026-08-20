package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStrSliceRcBoxIRX86_64 pins the #2649 follow-up that closes the LAST
// header-less string producer on the asm IR path: the str_slice op (`s[a:b]`, and
// everything built on it — `.trim()`, `.split()`). It used to allocate a
// raw 16-byte view box {data@0, len@8} with NO rc word, so box-8 was the previous
// heap block — incing such a box (e.g. a struct string-field alias-inc) clobbered the
// adjacent block. It now allocates a 24-byte box [rc=-1@base, data@base+8, len@base+16]
// (box = base+8, so data@box+0 / len@box+8 are unchanged) whose rc is the IMMORTAL
// sentinel (-1). The bytes stay SHARED with the source (zero-copy preserved), so the
// view must never free them: __fn___fern_str_free skips a negative rc, and
// __fn___fern_rc_inc skips it too (`js`). Net: every asm-IR string box now carries a
// valid rc word at box-8, so incing/decrementing ANY string (including a slice/trim
// VIEW) is sound.
//
// Two contracts:
//   - EMISSION: a slicing program emits the immortal-sentinel store `movq $-1,`
//     (the rc-headered view header) — the old header-less 16-byte view is gone.
//   - SAFETY-NET CHURN: a long trim/slice churn keeps a flat heap with zero
//     over-releases (a view is never freed, and nothing double-frees it), so the
//     underflow counter stays 0 and the value is correct -> exit 0.
func TestSelfHostStrSliceRcBoxIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantSentinel bool) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantSentinel && !bytes.Contains(asm, []byte("movq $-1,")) {
			t.Errorf("%s: expected `movq $-1,` (rc-headered immortal slice-view header), found none", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (99 = over-release detected by the rc-aware __fern_str_free)", name, code, want)
		}
	}

	// EMISSION: a direct string slice `s[1:4]` must build the rc-headered immortal
	// view box (movq $-1 sentinel). "hello"[1:4] = "ell", len 3 -> exit 3.
	run(t, `function main(): i32 { var s: string = "hello"; var t: string = s[1:4] + ""; return t.len(); }`,
		"slice-emits-rc-header", 3, true)

	// SAFETY-NET CHURN: 2,000,000 trim + slice iterations. trim() lowers to s[start:end]
	// (a slice view); each iteration builds a fresh immortal view. A view is never freed
	// (rc=-1), so no double-free can tick __rc_underflow(); the shared source
	// buffer is never reclaimed out from under a live view. r = "mid" len 3, so bad stays
	// 0, underflow 0 -> exit 0.
	run(t, `function churn(n: i32): i32 { var s: string = "  mid  "; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var r: string = s.trim(); if (r.len() != 3) { bad = 1; } var sl: string = s[2:5]; if (sl.len() != 3) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"slice-trim-churn-no-underflow", 0, true)
}
