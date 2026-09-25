package e2e

import (
	"strings"
	"testing"
)

// A `fip` function may call the builtins that allocate nothing (#9607): the
// byte-scan kernels, the bit counts, the pointer width, the heap counters and
// the clock. Each backend's verifyFipAllocs (E068) sees what they lower to, so
// this compiling at all is half the test; the other half is that the call
// allocates nothing and answers as the unannotated code does.
const fipBuiltinsSrc = `import "std/i32";

fip function scan(s: string, set: u8[]): i32 {
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
    print(r.to_string());
    return 0;
}
`

const fipBuiltinsWant = "3464"

func TestFipCallsNonAllocatingBuiltins(t *testing.T) {
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86Native(t, fipBuiltinsSrc)
		if code != 0 || strings.TrimSpace(out) != fipBuiltinsWant {
			t.Fatalf("exit %d, output %q, want 0 and %s (90: the calls allocated)", code, out, fipBuiltinsWant)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		bin, qemu := compileArm64FreeOn(t, fipBuiltinsSrc)
		cmd := runArm64Bin(qemu, bin)
		out, _ := cmd.Output()
		if code := cmd.ProcessState.ExitCode(); code != 0 || strings.TrimSpace(string(out)) != fipBuiltinsWant {
			t.Fatalf("exit %d, output %q, want 0 and %s (90: the calls allocated)", code, out, fipBuiltinsWant)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if out := strings.TrimSpace(compileAndRunWasmCapture(t, fipBuiltinsSrc)); out != fipBuiltinsWant {
			t.Fatalf("output %q, want %s", out, fipBuiltinsWant)
		}
	})
}
