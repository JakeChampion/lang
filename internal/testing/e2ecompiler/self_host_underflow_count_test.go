package e2ecompiler

import "testing"

const underflowCountSrc = `import "std/i32";
@noinline function arr(): i32 {
    let a: u8[] = __alloc_u8(16);
    let b: u8[] = __rc_inc(a);
    __rc_dec(a);
    __rc_dec(a);
    __rc_dec(a);
    return b.len() - 16;
}
@noinline function str(k: i32): i32 {
    let s: string = "value " + k.to_string();
    __store_i32(__raw_data(s) - 24, 0);
    return s.len() - 11;
}
function main(): i32 {
    let x: i32 = arr();
    let c1: i32 = __rc_underflow_count();
    let y: i32 = str(12345);
    let c2: i32 = __rc_underflow_count();
    return c1 * 10 + (c2 - c1) + x + y;
}`

func TestSelfHostUnderflowCounted(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, underflowCountSrc, target, uncertified)
			if code != 31 || stderr != "" {
				t.Errorf("exit=%d stderr=%q, want exit 31 (three array underflows, then one string underflow) and silence", code, stderr)
			}
		})
	}
}
