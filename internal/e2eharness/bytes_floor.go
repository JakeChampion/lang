package e2eharness

// BytesFloorProbe exercises the runtime's bytes floor (#9853): `__str_bytes`
// answers the address of a string's bytes for a string carried inline in
// its word (spilled into the caller's scratch) and for a heap string, and
// answers 0 for the inline case when no scratch is given; `__arr_set_len`
// shortens a byte array to the bytes a read filled. With rawStores the
// array is filled through its data-pointer cast and `__store_u8`, the way
// the Go compiler's socket bodies fill a read buffer; without, it is a
// literal, since the self-host keeps a byte array one word per element.
// Exit 42 iff every step holds; each failing step has its own code.
func BytesFloorProbe(rawStores bool) string {
	fill := `    var b: u8[] = __alloc_u8(4);
    var bp: usize = b as usize;
    __store_u8(bp, 104);
    __store_u8(bp + 1, 105);
    __store_u8(bp + 2, 106);
    __store_u8(bp + 3, 107);
`
	if !rawStores {
		fill = `    var b: u8[] = [104u8, 105u8, 106u8, 107u8];
`
	}
	return `function main(): i32 {
` + fill + `    if (b.len() != 4 || b[0] != 104u8 || b[3] != 107u8) { return 1; }
    __arr_set_len(b, 2);
    if (b.len() != 2 || b[1] != 105u8) { return 2; }
    var scratch: usize = __alloc(16);
    var short: string = "hi";
    var sp: usize = __str_bytes(short, scratch);
    if (sp == (0 as usize) || __load_u8(sp) != 104 || __load_u8(sp + 1) != 105) { return 3; }
    var long: string = "a string of more than seven bytes";
    var lp: usize = __str_bytes(long, scratch);
    if (lp == (0 as usize) || __load_u8(lp) != 97 || __load_u8(lp + (long.len() as usize) - 1) != 115) { return 4; }
    if (__str_bytes(long, 0 as usize) != lp) { return 5; }
    var bare: usize = __str_bytes(short, 0 as usize);
    if (bare != (0 as usize) && __load_u8(bare) != 104) { return 6; }
    __free(scratch, 16);
    return 42;
}
`
}
