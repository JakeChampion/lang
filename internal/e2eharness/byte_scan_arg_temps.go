package e2eharness

// ByteScanArgTempsProgram hands each byte-scan builtin a fresh temporary
// (an array literal, a call's result, a concatenation) 100 times and exits
// 0 when every answer is right; a leak census over it must balance.
const ByteScanArgTempsProgram = `const SET: u8[] = [0u8, 1u8, 0u8];
function fresh(): u8[] {
    return __alloc_u8(4);
}
function main(): i32 {
    let s: string = "abc";
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 100) {
        if (__sum_bytes_array([1u8, 2u8]) != 3) { return 2; }
        if (__bsd_sum_bytes(fresh(), 0) != 0) { return 3; }
        if (__crc32_cksum_array(0, fresh()) != 0) { return 4; }
        if (__count_runs_bytes([0u8, 1u8, 0u8, 1u8], 0, [0u8, 1u8]) != 2) { return 5; }
        if (__mismatch_bytes([1u8, 2u8], 0, [1u8, 3u8], 0, 2) != 1) { return 6; }
        sum = sum + __memchr_bytes([1u8, 2u8], 2, 0);
        sum = sum + __rmemchr_bytes([2u8, 1u8], 2, 1);
        sum = sum + __count_byte_bytes(fresh(), 0);
        sum = sum + __memchr(s + "x", 120, 0);
        sum = sum + __rmemchr(s + "x", 97, 3);
        sum = sum + __count_byte(s + "a", 97);
        sum = sum + __ascii_run(s + "d", 0);
        sum = sum + __scan_set(" a", 0, SET);
        sum = sum + __scan_set_bytes([32u8, 1u8], 0, SET);
        sum = sum + __count_runs(s + "e", 0, SET);
        sum = sum + __mismatch(s + "y", 0, s + "z", 0, 4);
        sum = sum + __sum_bytes(s + "") % 2;
        sum = sum + __bsd_sum(s + "", 0) % 2;
        sum = sum + __crc32_cksum(0, s + "") % 2;
        i = i + 1;
    }
    if (sum != 100 * (1 + 0 + 4 + 3 + 0 + 2 + 4 + 2 + 1 + 0 + 3 + 0 + 0 + 0)) {
        return 1;
    }
    return 0;
}`
