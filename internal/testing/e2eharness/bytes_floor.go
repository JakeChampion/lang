package e2eharness

// BytesFloorProbe exercises the runtime's bytes floor (#9853): `__str_bytes`
// answers the address of a string's bytes for a string carried inline in
// its word (spilled into the caller's scratch) and for a heap string, and
// answers 0 for the inline case when no scratch is given; `__arr_set_len`
// shortens a byte array to the bytes a read filled. Exit 42 iff every step
// holds; each failing step has its own code.
func BytesFloorProbe() string {
	return `function main(): i32 {
    let b: u8[] = [104u8, 105u8, 106u8, 107u8];
    if (b.len() != 4 || b[0] != 104u8 || b[3] != 107u8) { return 1; }
    __arr_set_len(b, 2);
    if (b.len() != 2 || b[1] != 105u8) { return 2; }
    let scratch: usize = __alloc(16);
    let short: string = "hi";
    let sp: usize = __str_bytes(short, scratch);
    if (sp == (0 as usize) || __load_u8(sp) != 104 || __load_u8(sp + 1) != 105) { return 3; }
    let long: string = "a string of more than seven bytes";
    let lp: usize = __str_bytes(long, scratch);
    if (lp == (0 as usize) || __load_u8(lp) != 97 || __load_u8(lp + (long.len() as usize) - 1) != 115) { return 4; }
    if (__str_bytes(long, 0 as usize) != lp) { return 5; }
    let bare: usize = __str_bytes(short, 0 as usize);
    if (bare != (0 as usize) && __load_u8(bare) != 104) { return 6; }
    __free(scratch, 16);
    return 42;
}
`
}
