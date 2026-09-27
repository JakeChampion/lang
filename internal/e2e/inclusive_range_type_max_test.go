package e2e

import "testing"

// An inclusive range whose HIGH is its type's maximum ends after HIGH instead
// of wrapping to the minimum and running forever (#10359), at every width the
// native front end types a range variable at. The i32 cases are the
// inclusive_range_type_max fixture, which the self-host legs run too.
func TestInclusiveRangeAtTypeMaxEnds(t *testing.T) {
	assertExitsZeroEverywhere(t, `function main(): i32 {
    var n: i32 = 0;
    for a in 254u8..=255u8 { n = n + 1; }
    if (n != 2) { return 1; }
    var last: u8 = 0u8;
    n = 0;
    for b in 0u8..=255u8 { n = n + 1; last = b; }
    if (n != 256 || last != 255u8) { return 2; }
    n = 0;
    for c in 0u8..=0u8 { n = n + 1; }
    for e in 255u8..=254u8 { n = n + 100; }
    if (n != 1) { return 3; }
    n = 0;
    for k in 250u8..=255u8 {
        if (k == 252u8) { continue; }
        if (k == 255u8) { n = n + 1000; continue; }
        n = n + 1;
    }
    if (n != 1004) { return 4; }
    n = 0;
    for k2 in 253u8..=255u8 {
        n = n + 1;
        if (k2 == 254u8) { break; }
    }
    if (n != 2) { return 5; }
    n = 0;
    for d in 4294967294u32..=4294967295u32 { n = n + 1; }
    if (n != 2) { return 6; }
    n = 0;
    for f in 9223372036854775806i64..=9223372036854775807i64 { n = n + 1; }
    if (n != 2) { return 7; }
    n = 0;
    for g in 18446744073709551614u64..=18446744073709551615u64 { n = n + 1; }
    if (n != 2) { return 8; }
    return 0;
}
`)
}
