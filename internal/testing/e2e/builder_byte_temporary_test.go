package e2e

import "testing"

const builderByteTemporaryProgram = `function inspect(bytes: u8[]): i32 {
  if (bytes.len() != 3) { return 1; }
  if (bytes[0] != 255 as u8 || bytes[1] != 0 as u8 || bytes[2] != 128 as u8) { return 2; }
  return 0;
}
function length(bytes: u8[]): i32 { return bytes.len(); }
function main(): i32 {
  let b = buf_new(3);
  for iteration in 0..64 {
    buf_push_byte(b, 255); buf_push_byte(b, 0); buf_push_byte(b, 128);
    if (inspect(buf_take_bytes(b)) != 0) { return 1; }
    buf_push_byte(b, 128);
    buf_take_bytes(b);
    if (length(buf_take_bytes(b)) != 0) { return 2; }
  }
  buf_free(b);
  return 0;
}`

func TestBuilderByteTemporaryOwnershipCensus(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", "", runLeakCheckX86_64},
		{"arm64", "", runLeakCheckArm64},
		{"wasm", "0\n", func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, diagnostic, code := tc.run(t, builderByteTemporaryProgram)
			if code != 0 {
				t.Fatalf("fresh byte arguments: exit %d\n%s\n%s", code, out, diagnostic)
			}
			if out != tc.want {
				t.Fatalf("fresh byte argument contents failed: %q", out)
			}
			allocs, frees, live := parseLeakCheckLine(t, diagnostic)
			if allocs != frees || live != 0 {
				t.Fatalf("fresh byte arguments leaked: %s", diagnostic)
			}
		})
	}
}
