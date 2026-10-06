package e2ecompiler

import (
	"strings"
	"testing"
)

// The FP __c_call shims hand the C callee's result back where a Fern caller
// reads every result, %rax / x0, an f32 widened first to the f64 a Fern f32
// is held as (#11485). The x86-64 shims are also run, under dlopen, by the
// std/jni FP tests in internal/testing/e2e/shared_lib_test.go; a static arm64 program
// has no C function to call, so its shims are pinned here by their text.
func TestSelfHostCCallFPShimsReturnInTheResultRegister(t *testing.T) {
	cli := newStrictCLI(t)
	src := `function d(p: usize): f64 { return __c_call1_f64(p, 7 as usize); }
function f(p: usize): f32 { return __c_call0_f32(p); }
function main(): i32 { return (d(0 as usize) + (f(0 as usize) as f64)) as i32; }
`
	cases := []struct {
		target, call string
		f64, f32     []string
	}{
		{"x86-64-linux", "call *%r11", []string{"movq %xmm0, %rax"}, []string{"cvtss2sd %xmm0, %xmm0", "movq %xmm0, %rax"}},
		{"arm64-linux", "blr x9", []string{"fmov x0, d0"}, []string{"fcvt d0, s0", "fmov x0, d0"}},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			asm := cli.emit(t, c.target, src)
			for shim, want := range map[string][]string{"__fn___c_call1_f64": c.f64, "__fn___c_call0_f32": c.f32} {
				body := shimBody(asm, shim)
				if body == "" {
					t.Fatalf("no %s in the emitted program", shim)
				}
				_, after, ok := strings.Cut(body, c.call)
				if !ok {
					t.Fatalf("%s makes no %q:\n%s", shim, c.call, body)
				}
				var got []string
				for _, l := range strings.Split(after, "\n") {
					l = strings.TrimSpace(l)
					if l != "" && !strings.HasPrefix(l, "movq %rbp") && !strings.HasPrefix(l, "popq") && !strings.HasPrefix(l, "ldp") && l != "ret" {
						got = append(got, l)
					}
				}
				if strings.Join(got, "; ") != strings.Join(want, "; ") {
					t.Errorf("%s after the call: %q, want %q", shim, got, want)
				}
			}
		})
	}
}

// shimBody is the text from label's line to its first ret.
func shimBody(asm, label string) string {
	_, rest, ok := strings.Cut(asm, "\n"+label+":\n")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(rest, "\n    ret\n")
	return body
}
