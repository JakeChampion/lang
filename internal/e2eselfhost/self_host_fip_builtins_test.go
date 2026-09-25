package e2eselfhost

import (
	"os/exec"
	"testing"
)

// The self-host half of internal/e2e's TestFipCallsNonAllocatingBuiltins
// (#9607): irfipverify's E068 sees what each admitted builtin lowers to, and
// the calls allocate nothing (90) and answer as native does (3464 % 100).
const selfHostFipBuiltinsSrc = `fip function scan(s: string, set: u8[]): i32 {
    return __memchr(s, 44, 0) + __rmemchr(s, 44, s.len()) + __ascii_run(s, 0)
        + __count_byte(s, 44) + __sum_bytes(s) + __scan_set(s, 0, set) + __bsd_sum(s, 0)
        + __count_runs(s, 0, set) + (__crc32_cksum(0, s) % 1000);
}

fip function bits(x: u32, y: u64): i32 {
    return __clz32(x) + __ctz32(x) + __popcount32(x) + __clz64(y) + __ctz64(y) + __popcount64(y)
        + __ptr_width() / __ptr_width();
}

fip function observe(): i32 {
    var t: i64 = monotonic_ns();
    var bumped: i64 = __heap_bump_bytes() - __heap_bump_bytes();
    var count: i64 = __heap_alloc_count() - __heap_alloc_count();
    if (monotonic_ns() < t) { return 1; }
    return (bumped + count) as i32;
}

function main(): i32 {
    var set: u8[] = [44 as u8];
    var s: string = "a,b,,c";
    var at: i64 = __heap_alloc_count();
    var r: i32 = scan(s, set) + bits(12 as u32, 12 as u64) + observe();
    if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
    return r % 100;
}
`

func TestSelfHostFipCallsNonAllocatingBuiltins(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeEnumMapSrc(t, "fip_builtins", selfHostFipBuiltinsSrc)
	t.Run("x86-64", func(t *testing.T) {
		stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src), nil)
		if exit != 64 {
			t.Fatalf("exit = %d, want 64 (90: the calls allocated)\n%s", exit, stderr)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi"))
		if exit != 64 {
			t.Fatalf("exit = %d, want 64 (90: the calls allocated)\n%s", exit, stderr)
		}
	})
}
