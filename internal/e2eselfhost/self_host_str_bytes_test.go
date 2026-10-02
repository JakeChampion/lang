package e2eselfhost

import "testing"

// `__str_bytes` and `__raw_data` answer the address of a string's bytes on
// every target (#10586). A register-backend string box keeps a data pointer in
// slot 0, but a wasm string is one block, its length and then its bytes at +4,
// so reading slot 0 there answered the length and every byte read came back 0.
const strBytesProg = `@noinline function first_byte(s: string): i32 {
    let scratch: usize = __alloc(16);
    let c: i32 = __load_u8(__str_bytes(s, scratch));
    __free(scratch, 16);
    return c;
}
@noinline function last_byte(s: string): i32 {
    return __load_u8(__raw_data(s) + ((s.len() - 1) as usize));
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    let long: string = "a string of more than seven bytes";
    print_int(first_byte("hi")); print("");
    print_int(first_byte(long)); print("");
    print_int(last_byte(long)); print("");
    return 0;
}
`

// Every target emits str_data.
func TestSelfHostStrBytesEveryTarget(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			got, _ := routedMapRun(t, h.cli, h.stdlib, strBytesProg, target)
			if got != "104\n97\n115" {
				t.Fatalf("%s: got %q, want 104, 97, 115", target, got)
			}
		})
	}
}
