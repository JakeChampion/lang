package e2eharness

import (
	"fmt"
	"hash/crc32"
	"strings"
)

type crcVariant struct {
	builtin string
	state   string
	ctor    string
	dense   int
	update  func(crc uint32, data []byte) uint32
	finish  string
}

func cksumUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc ^= uint32(b) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func ieeeUpdate(crc uint32, data []byte) uint32 {
	return ^crc32.Update(^crc, crc32.IEEETable, data)
}

// CRC32ArraySource checks __crc32_cksum_array, the MSB-first POSIX cksum register.
func CRC32ArraySource(compiled bool) string {
	return crc32ArraySource(compiled, crcVariant{builtin: "__crc32_cksum_array", state: "Cksum", ctor: "cksum_new", dense: 200, update: cksumUpdate})
}

// CRC32IEEEArraySource checks __crc32_ieee_array, the reflected gzip register, and
// that hash.crc32_of agrees with Go's hash/crc32.
func CRC32IEEEArraySource(compiled bool) string {
	return crc32ArraySource(compiled, crcVariant{builtin: "__crc32_ieee_array", state: "Crc32", ctor: "crc32_new", dense: 300, update: ieeeUpdate,
		finish: `if ((hash.crc32_of(bytes) as i32) != (crc(0 - 1, bytes) ^ (0 - 1))) { return 8; }`})
}

func crc32ArraySource(compiled bool, v crcVariant) string {
	lengths := []int{0, 1, 7, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65, 111, 112, 113, 127, 128, 129, 191, 192, 193, 255, 256}
	if compiled {
		lengths = nil
		for n := 0; n <= v.dense; n++ {
			lengths = append(lengths, n)
		}
		for _, n := range []int{255, 256, 257, 1023, 1024, 1025, 65535, 65536, 65537} {
			if n > v.dense {
				lengths = append(lengths, n)
			}
		}
	}
	seeds := []uint32{0, 1, 0xffffffff, 0x80000000, 0xdeadbeef}
	var ns, ss, expected strings.Builder
	for _, n := range lengths {
		fmt.Fprintf(&ns, "%d,", n)
		data := make([]byte, n)
		for at := range data {
			data[at] = byte(at*73 + 19)
		}
		for _, seed := range seeds {
			fmt.Fprintf(&expected, "%di64 as i32,", int64(int32(v.update(seed, data))))
		}
	}
	for _, seed := range seeds {
		fmt.Fprintf(&ss, "%di64 as i32,", int64(int32(seed)))
	}
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
	return strings.NewReplacer("LENGTHS", ns.String(), "SEEDS", ss.String(), "// Expected batches", batches.String(),
		"// Compiled probe", probe, "// Finish check", v.finish, "CRC_BUILTIN", v.builtin, "CRC_STATE", v.state, "CRC_NEW", v.ctor).Replace(crc32ArrayProgram)
}

const crc32ArrayProgram = `import "std/hash";
import "std/string";
// Expected batches
fip function crc(seed: i32, bytes: u8[]): i32 {
    return CRC_BUILTIN(seed, bytes);
}
fip function crc_view(seed: i32, bytes: [u8]): i32 {
    return CRC_BUILTIN(seed, bytes);
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
function shifted(n: i32, off: i32): u8[] {
    let bytes: u8[] = __alloc_u8(off + n + 3);
    let at: i32 = 0;
    while (at < off + n + 3) {
        bytes = bytes.with(at, 165 as u8);
        at = at + 1;
    }
    at = 0;
    while (at < n) {
        bytes = bytes.with(off + at, (at * 73 + 19) as u8);
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
        let off: i32 = n % 7 + 1;
        let cut: i32 = n / 3;
        let wide: u8[] = shifted(n, off);
        let text: string = string_from_bytes_unchecked(wide) + "";
        for seed in seeds {
            if (index % 100 == 0) { expected = expected_batch(index / 100); }
            let want: i32 = expected[index % 100];
            if (crc(seed, bytes) != want) { return 1; }
            if (crc(crc(seed, left), right) != want) { return 2; }
            if (crc_view(seed, wide[off:off + n]) != want) { return 5; }
            if (crc_view(crc_view(seed, wide[off:off + cut]), wide[off + cut:off + n]) != want) { return 6; }
            if (crc_view(crc_view(seed, text.as_bytes()[off:off + cut]), text.as_bytes()[off + cut:off + n]) != want) { return 7; }
            index = index + 1;
        }
        let array: hash.CRC_STATE = hash.CRC_NEW().update(left).update(right);
        let view: hash.CRC_STATE = hash.CRC_NEW().update(bytes);
        if (array.finish() != view.finish() || array.byte_count() != (n as u64)) { return 3; }
        // Finish check
    }
    let bytes: u8[] = pattern(256);
    let held: u8[] = bytes;
    let original: i32 = crc(123, held);
    // Compiled probe
    bytes = bytes.with(255, 0 as u8);
    if (crc(123, held) != original || crc(123, bytes) == original) { return 4; }
    return 0;
}`
