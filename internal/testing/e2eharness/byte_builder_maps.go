package e2eharness

import "strings"

// ByteBuilderMapsSource compares the three raw builders with element-wise
// oracles. Native census runs also verify pushes into reserved space allocate
// neither temporary arrays nor copies of borrowed inputs.
func ByteBuilderMapsSource(allocations bool) string {
	probe := ""
	if allocations {
		probe = `let reserved = buf_new(10000);
    let before = __heap_alloc_count();
    let repeat = 0;
    while (repeat < 1000) {
        push(reserved, 0, alias, held);
        push(reserved, 1, alias, held);
        push(reserved, 2, alias, held);
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before || buf_len(reserved) != 8000) { buf_free(reserved); return 5; }
    buf_free(reserved);`
	}
	return strings.Replace(byteBuilderMapsProgram, "// Allocation probe", probe, 1)
}

const byteBuilderMapsProgram = `function push(h: usize, kind: i32, src: u8[], table: u8[]): void {
    if (kind == 0) { buf_push_bytes_mapped(h, src, table); }
    else if (kind == 1) { buf_push_bytes_filtered(h, src, table); }
    else { buf_push_bytes_expanded(h, src, table); }
}
function slow(kind: i32, src: u8[], table: u8[]): u8[] {
    let out: u8[] = [];
    for byte in src {
        let c = byte as i32;
        if (kind == 0) {
            if (c < table.len()) { out = out.append(table[c]); }
            else { out = out.append(byte); }
        } else if (kind == 1) {
            if (c >= table.len() || table[c] == 0 as u8) { out = out.append(byte); }
        } else {
            let at = c * 8;
            if (at + 8 <= table.len()) {
                let n = table[at] as i32;
                if (n > 7) { n = 7; }
                let k = 0;
                while (k < n) { out = out.append(table[at + k + 1]); k = k + 1; }
            } else { out = out.append(byte); }
        }
    }
    return out;
}
function equal(a: u8[], b: u8[]): boolean {
    if (a.len() != b.len()) { return false; }
    let i = 0;
    while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; }
    return true;
}
function main(): i32 {
    for n in [0, 1, 3, 4, 7, 8, 15, 16, 31, 32, 33, 256, 257] {
        let src: u8[] = __alloc_u8(n);
        let i = 0;
        while (i < n) { src = src.with(i, (i % 256) as u8); i = i + 1; }
        for width in [0, 1, 7, 8, 9, 127, 255, 256, 257, 2047, 2048, 2056] {
            let table: u8[] = __alloc_u8(width);
            i = 0;
            while (i < width) { table = table.with(i, ((i * 37 + i / 8) % 256) as u8); i = i + 1; }
            for kind in [0, 1, 2] {
                let h = buf_new(1);
                buf_push_byte(h, 255);
                push(h, kind, src, table);
                buf_push_byte(h, 128);
                let got = buf_take_bytes(h);
                let want = slow(kind, src, table);
                if (got.len() != want.len() + 2 || got[0] != 255 as u8 || got[got.len() - 1] != 128 as u8) { buf_free(h); return 1; }
                i = 0;
                while (i < want.len()) { if (got[i + 1] != want[i]) { buf_free(h); return 2; } i = i + 1; }
                push(h, kind, src, table);
                let next = buf_take_bytes(h);
                buf_free(h);
                if (!equal(next, want) || got[0] != 255 as u8) { return 3; }
            }
        }
    }
    // Aliases are retained across the push and later source mutations.
    let src: u8[] = [0 as u8, 255 as u8, 128 as u8];
    let alias = src;
    let table: u8[] = [128 as u8];
    let held = table;
    let h = buf_new(0);
    push(h, 0, src, table);
    src = src.with(0, 1 as u8);
    table = table.with(0, 2 as u8);
    let got = buf_take_bytes(h);
    buf_free(h);
    if (!equal(got, [128 as u8, 255 as u8, 128 as u8]) || alias[0] != 0 as u8 || held[0] != 128 as u8) { return 4; }
    // Allocation probe
    return 0;
}`
