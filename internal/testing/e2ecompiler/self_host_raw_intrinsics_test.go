package e2ecompiler

import "testing"

const rawIntrinsicsProg = `function build_str(): string {
    let p: usize = __raw_alloc(3);
    __raw_store8(p, 0, 72);
    __raw_store8(p, 1, 105);
    __raw_store8(p, 2, 33);
    return __raw_string(p, 3);
}
function main(): i32 {
    let s: string = build_str();
    let p2: usize = __raw_alloc(16);
    __raw_store_ptr(p2, 0, 1234 as usize);
    __raw_store8(p2, 8, 200);
    let w: usize = __raw_load_ptr(p2, 0);
    let b: i32 = __raw_load8(p2, 8);
    return s.len() + (s[0] as i32) + ((w as i32) - 1234) + (b - 200);
}`

func TestSelfHostRawIntrinsicsX86_64(t *testing.T) {
	cli := newStrictRuntimeCLI(t)
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", rawIntrinsicsProg)); code != 75 {
		t.Errorf("raw-intrinsic probe exited %d, want 75 (s.len()=3 + s[0]=72)", code)
	}
}
