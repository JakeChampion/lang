package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// Exercise the runtime ABI directly: ordinary record shapes do not reach the
// large tier. Keep the emitted allocator, release and diagnostic bodies intact.
func TestSelfHostAllocReuseLargeDonor(t *testing.T) {
	cli := buildRuntimeCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { let p: usize = __raw_arr_box(1); __fern_rc_dec(p); return 0; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		cap, want int
	}{
		{0, 1}, {3, 1}, {65532, 1}, {65533, 1},
		{65534, 1}, {70000, 1}, {65533, 65533}, {3, 70000},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			gcc, runner := x86_64Tooling(t)
			armRunner := ""
			if target == "arm64-linux" {
				gcc, armRunner = arm64Tooling(t)
				runner = nil
				if armRunner != "" {
					runner = []string{armRunner}
				}
			}
			modes := []string{"normal", "quarantine", "sanitized"}
			if target == "arm64-linux" {
				modes = append(modes, "hardened")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					env := []string{"FERN_LEAKCHECK=1"}
					if mode == "quarantine" {
						env = append(env, "FERN_RC_FREE_DEBUG=1")
					}
					if mode == "sanitized" {
						env = append(env, "FERN_SANITIZE=1")
					}
					asmPath := cli.emit(t, src, target, env...)
					if mode == "hardened" {
						cmd := runX86_64Bin(cli.runner, cli.bin, "-harden", "-target", target, "-emit", "asm", "-o", asmPath, src, cli.stdlib)
						cmd.Env = append(os.Environ(), env...)
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("hardened emit: %v\n%s", err, out)
						}
					}
					raw, err := os.ReadFile(asmPath)
					if err != nil {
						t.Fatal(err)
					}
					text := string(raw)
					hardenedFrame := ""
					if mode == "hardened" {
						at := strings.Index(text, "__fn___fern_alloc_reuse:\n")
						if at < 0 {
							t.Fatal("missing allocation reuse helper")
						}
						end := strings.Index(text[at:], ".Lsarelo_fresh:\n")
						if end < 0 {
							t.Fatal("missing allocation reuse fallback")
						}
						frame := text[at : at+end]
						hardenedFrame = frame
						protected := regexp.MustCompile(`(?s)paciasp\n\s+stp x29, x30, \[sp, #-16\]!\n\s+bl __fn___fern_arr_dec\.r\n\s+ldp x29, x30, \[sp\], #16\n\s+autiasp`)
						if !protected.MatchString(frame) {
							t.Fatalf("unprotected mismatch return-address frame:\n%s", frame)
						}
					}
					start := strings.Index(text, "\n_start:\n")
					if start < 0 {
						t.Fatal("missing entry")
					}
					start++
					section := regexp.MustCompile(`(?m)^\s*\.(section|bss|data|text)\b`).FindStringIndex(text[start:])
					if section == nil {
						t.Fatal("missing section after entry")
					}
					entries := []bool{false}
					if target == "x86-64-linux" {
						entries = append(entries, true)
					}
					for _, stack := range entries {
						for _, tc := range cases {
							t.Run(fmt.Sprintf("%d-to-%d-stack-%t", tc.cap, tc.want, stack), func(t *testing.T) {
								entry := allocReuseDonorProbe(target, tc.cap, tc.want, stack, mode == "quarantine" || mode == "sanitized")
								dir := t.TempDir()
								asm, bin := filepath.Join(dir, "probe.s"), filepath.Join(dir, "probe")
								if err := os.WriteFile(asm, []byte(text[:start]+entry+text[start+section[0]:]), 0o644); err != nil {
									t.Fatal(err)
								}
								if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asm, "-o", bin).CombinedOutput(); err != nil {
									t.Fatalf("link: %v\n%s", err, out)
								}
								stderr, code := runCapturingStderr(t, ssaBackendTarget{runner: runner}, bin)
								count := 2
								if tc.cap == 0 || tc.cap == tc.want {
									count = 1
								}
								if tc.cap > 0 && tc.cap != tc.want {
									count++ // Reallocate the donor's capacity to check its pool.
								}
								want := fmt.Sprintf("leakcheck: allocs=%d frees=%d live_bytes=0", count, count)
								if code != 0 || !strings.Contains(stderr, want) || strings.Contains(stderr, "fern-sanitizer:") {
									t.Fatalf("exit=%d, want balanced %q\n%s", code, want, stderr)
								}
								if mode == "hardened" && tc.cap == 65533 && tc.want == 1 && cpuEnforces(armRunner, " paca") {
									assembly, err := os.ReadFile(asm)
									if err != nil {
										t.Fatal(err)
									}
									checkAllocReuseDonorPAC(t, gcc, armRunner, dir, string(assembly), hardenedFrame, want)
								}
							})
						}
					}
				})
			}
		})
	}
}

func checkAllocReuseDonorPAC(t *testing.T, gcc, runner, dir, assembly, frame, want string) {
	t.Helper()
	call := "    bl __fn___fern_arr_dec.r\n"
	corrupt := call + "    ldr x4, [sp, #8]\n    eor x4, x4, #0x40000000000000\n    str x4, [sp, #8]\n"
	changed := strings.Replace(frame, call, corrupt, 1)
	if changed == frame {
		t.Fatal("failed to corrupt the mismatch frame's saved signature")
	}
	for _, strip := range []bool{false, true} {
		body := changed
		if strip {
			body = strings.Replace(body, "    autiasp\n", "    xpaclri\n", 1)
		}
		asm, bin := filepath.Join(dir, fmt.Sprintf("pac-%t.s", strip)), filepath.Join(dir, fmt.Sprintf("pac-%t", strip))
		if err := os.WriteFile(asm, []byte(strings.Replace(assembly, frame, body, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asm, "-o", bin).CombinedOutput(); err != nil {
			t.Fatalf("PAC control link: %v\n%s", err, out)
		}
		out, err := runArm64Bin(runner, bin).CombinedOutput()
		if !strip && err == nil {
			t.Fatalf("mismatch authenticated a corrupted saved signature:\n%s", out)
		}
		if strip && (err != nil || !strings.Contains(string(out), want)) {
			t.Fatalf("stripping the corrupted signature changed its return address: %v\n%s", err, out)
		}
	}
	t.Log("a corrupted saved PAC faults; stripping it restores the balanced return")
}

func allocReuseDonorProbe(target string, cap, want int, stack, quarantine bool) string {
	mismatch := cap > 0 && cap != want
	if target == "x86-64-linux" {
		s := "_start:\n    pushq %rbp\n    movq %rsp, %rbp\n    xorq %r12, %r12\n"
		if cap > 0 {
			s += fmt.Sprintf("    movq $%d, %%rdi\n    call __fern_arr_box\n    movq %%rax, %%r12\n", cap)
		}
		s += fmt.Sprintf("    movq %%r12, %%rax\n    movq $%d, %%rsi\n", want)
		if stack {
			s += "    pushq %rax\n    pushq %rsi\n    call __fn___fern_alloc_reuse\n    addq $16, %rsp\n"
		} else {
			s += "    call __fn___fern_alloc_reuse.r\n"
		}
		s += fmt.Sprintf("    cmpq $%d, -16(%%rax)\n    jne .Lprobe_fail\n    cmpl $1, -8(%%rax)\n    jne .Lprobe_fail\n", want)
		if cap == want {
			s += "    cmpq %r12, %rax\n    jne .Lprobe_fail\n"
		}
		if mismatch && quarantine {
			s += fmt.Sprintf("    cmpl $%d, -8(%%r12)\n    jne .Lprobe_fail\n", ast.RcPoison)
		}
		s += "    call __fn___fern_arr_dec.r\n"
		if mismatch {
			s += fmt.Sprintf("    movq $%d, %%rdi\n    call __fern_arr_box\n    cmpq %%r12, %%rax\n", cap)
			if quarantine {
				s += "    je .Lprobe_fail\n"
			} else {
				s += "    jne .Lprobe_fail\n"
			}
			s += "    call __fn___fern_arr_dec.r\n"
		}
		return s + "    xorq %r13, %r13\n    jmp .Lprobe_report\n.Lprobe_fail:\n    movq $91, %r13\n.Lprobe_report:\n    call __fern_lc_report\n    movq %r13, %rdi\n    movq $231, %rax\n    syscall\n"
	}
	s := "_start:\n    mov x20, #0\n"
	if cap > 0 {
		s += fmt.Sprintf("    movz x0, #%d\n    movk x0, #%d, lsl #16\n    bl __fern_arr_box\n    mov x20, x0\n", cap&65535, cap>>16)
	}
	s += fmt.Sprintf("    movz x1, #%d\n    movk x1, #%d, lsl #16\n    sub sp, sp, #32\n    str x20, [sp, #16]\n    str x1, [sp]\n    bl __fn___fern_alloc_reuse\n    add sp, sp, #32\n    ldur x2, [x0, #-16]\n    movz x3, #%d\n    movk x3, #%d, lsl #16\n    cmp x2, x3\n    b.ne .Lprobe_fail\n    ldur w2, [x0, #-8]\n    cmp w2, #1\n    b.ne .Lprobe_fail\n", want&65535, want>>16, want&65535, want>>16)
	if cap == want {
		s += "    cmp x0, x20\n    b.ne .Lprobe_fail\n"
	}
	if mismatch && quarantine {
		s += fmt.Sprintf("    ldur w2, [x20, #-8]\n    movz w3, #%d\n    movk w3, #%d, lsl #16\n    cmp w2, w3\n    b.ne .Lprobe_fail\n", ast.RcPoison&65535, ast.RcPoison>>16)
	}
	s += "    bl __fn___fern_arr_dec.r\n"
	if mismatch {
		s += fmt.Sprintf("    movz x0, #%d\n    movk x0, #%d, lsl #16\n    bl __fern_arr_box\n    cmp x0, x20\n", cap&65535, cap>>16)
		if quarantine {
			s += "    b.eq .Lprobe_fail\n"
		} else {
			s += "    b.ne .Lprobe_fail\n"
		}
		s += "    bl __fn___fern_arr_dec.r\n"
	}
	return s + "    mov x19, #0\n    b .Lprobe_report\n.Lprobe_fail:\n    mov x19, #91\n.Lprobe_report:\n    bl __fern_lc_report\n    mov x0, x19\n    mov x8, #93\n    svc #0\n"
}
