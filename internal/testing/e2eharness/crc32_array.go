package e2eharness

import (
	"fmt"
	"strings"
)

func CRC32ArraySource(compiled bool) string {
	lengths := []int{0, 1, 7, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65, 111, 112, 113, 127, 128, 129, 191, 192, 193, 255, 256}
	if compiled {
		lengths = nil
		for n := 0; n <= 200; n++ {
			lengths = append(lengths, n)
		}
		lengths = append(lengths, 255, 256, 257, 1023, 1024, 1025, 65535, 65536, 65537)
	}
	seeds := []uint32{0, 1, 0xffffffff, 0x80000000, 0xdeadbeef}
	var ns, ss, expected strings.Builder
	for _, n := range lengths {
		fmt.Fprintf(&ns, "%d,", n)
		for _, seed := range seeds {
			crc := seed
			for at := 0; at < n; at++ {
				crc ^= uint32(byte(at*73+19)) << 24
				for bit := 0; bit < 8; bit++ {
					if crc&0x80000000 != 0 {
						crc = crc<<1 ^ 0x04c11db7
					} else {
						crc <<= 1
					}
				}
			}
			fmt.Fprintf(&expected, "%di64 as i32,", int64(int32(crc)))
		}
	}
	for _, seed := range seeds {
		fmt.Fprintf(&ss, "%di64 as i32,", int64(int32(seed)))
	}
	// Keep every reference value, but distribute constants across functions
	// so this fixture does not exhaust the ARM assembler's literal pool.
	values := strings.Split(strings.TrimSuffix(expected.String(), ","), ",")
	var batches, dispatch strings.Builder
	for start := 0; start < len(values); start += 100 {
		end := min(start+100, len(values))
		batch := start / 100
		fmt.Fprintf(&batches, "function expected_%d(): i32[] { return [%s]; }\n", batch, strings.Join(values[start:end], ","))
		fmt.Fprintf(&dispatch, "if (batch == %d) { return expected_%d(); }\n", batch, batch)
	}
	fmt.Fprintf(&batches, "function expected_batch(batch: i32): i32[] { %s return []; }\n", dispatch.String())
	probe := ""
	if compiled {
		probe = `let before: i64 = __heap_alloc_count();
    let repeat: i32 = 0;
    while (repeat < 1000) {
        if (crc(123, held) != original) { return 10; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 11; }`
	}
	return strings.NewReplacer("LENGTHS", ns.String(), "SEEDS", ss.String(), "// Expected batches", batches.String(), "// Compiled probe", probe).Replace(crc32ArrayProgram)
}

const crc32ArrayProgram = `import "std/hash";
// Expected batches
fip function crc(seed: i32, bytes: u8[]): i32 {
    return __crc32_cksum_array(seed, bytes);
}
function copy_range(bytes: u8[], lo: i32, hi: i32): u8[] {
    let h: usize = buf_new(hi - lo);
    buf_push_bytes_range(h, bytes, lo, hi);
    let out: u8[] = buf_take_bytes(h);
    buf_free(h);
    return out;
}
function pattern(n: i32): u8[] {
    let bytes: u8[] = __alloc_u8(n);
    let at: i32 = 0;
    while (at < n) {
        bytes = bytes.with(at, (at * 73 + 19) as u8);
        at = at + 1;
    }
    return bytes;
}
function main(): i32 {
    let lengths: i32[] = [LENGTHS];
    let seeds: i32[] = [SEEDS];
    let expected: i32[] = [];
    let index: i32 = 0;
    for n in lengths {
        let bytes: u8[] = pattern(n);
        let left: u8[] = copy_range(bytes, 0, n / 2);
        let right: u8[] = copy_range(bytes, n / 2, n);
        for seed in seeds {
            if (index % 100 == 0) { expected = expected_batch(index / 100); }
            if (crc(seed, bytes) != expected[index % 100]) { return 1; }
            if (crc(crc(seed, left), right) != expected[index % 100]) { return 2; }
            index = index + 1;
        }
        let array: hash.Cksum = hash.cksum_new().update_array(left).update_array(right);
        let view: hash.Cksum = hash.cksum_new().update_bytes(bytes);
        if (array.finish() != view.finish() || array.len() != (n as u64)) { return 3; }
    }
    let bytes: u8[] = pattern(256);
    let held: u8[] = bytes;
    let original: i32 = crc(123, held);
    // Compiled probe
    bytes = bytes.with(255, 0 as u8);
    if (crc(123, held) != original || crc(123, bytes) == original) { return 4; }
    return 0;
}`
