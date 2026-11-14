package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func stringReplaceLengthValidSource() string {
	cases := []struct {
		source, old, replacement string
		limit                    int
	}{
		{"aaaa", "a", "b", 2},
		{"a.b.c", ".", "-", 1},
		{"hello", "z", "x", 3},
		{"xx", "x", "yy", 1},
		{"foofoofoo", "foo", "bar", 2},
		{"", "a", "b", 3},
		{"abc", "", "x", 3},
		{"aaa", "a", "", 2},
		{"aaa", "aa", "b", 2147483647},
		{"aaaa", "aa", "b", 1},
		{"aaaa", "a", "", 2147483647},
		{"éé🙂é", "é", "𝄞", 2},
		{"a\x00a", "a", "b\x00", 2},
		{"aaaa", "a", "b", 0},
		{"aaaa", "a", "b", -1},
		{"aaaa", "a", "b", -2147483648},
		{"same", "same", "same", 2147483647},
		{"abba", "b", "xyz", 2147483647},
	}
	var source strings.Builder
	source.WriteString(`import "std/string";
@noinline function replaced(s: string, old: string, replacement: string, limit: i32): string {
  let input = s.repeat(2);
  return input.replace_n(old, replacement, limit);
}
@noinline function check(s: string, old: string, replacement: string, limit: i32, want: string): boolean {
  let result = replaced(s, old, replacement, limit);
  let churn = "heap churn".repeat(32);
  return churn.len() == 320 && result == want;
}
function main(): i32 {
`)
	for i, tc := range cases {
		want := tc.source + tc.source
		if tc.old != "" && tc.limit > 0 {
			want = strings.Replace(want, tc.old, tc.replacement, tc.limit)
		}
		fmt.Fprintf(&source, "  if (!check(%s, %s, %s, %d, %s)) { return %d; }\n",
			strconv.Quote(tc.source), strconv.Quote(tc.old), strconv.Quote(tc.replacement), tc.limit, strconv.Quote(want), i+1)
	}
	source.WriteString(`  if ("x--x".replace("x", "yz") != "yz--yz") { return 19; }
  let input = "a".repeat(2048);
  let replacement = "b".repeat(2097152);
  if (input.replace_n("a", replacement, 0) != input) { return 20; }
  if (input.replace_n("z", replacement, 2147483647) != input) { return 21; }
  let capped = input.replace_n("a", replacement, 1);
  if (capped.len() != 2099199 || capped[2097151] != b'b' || capped[2099198] != b'a') { return 22; }
  let large = "a".repeat(1024).replace("a", "b".repeat(1024));
  if (large.len() != 1048576 || large[0] != b'b' || large[1048575] != b'b') { return 23; }
  print("replace lengths ok");
  return 0;
}
`)
	return source.String()
}

func TestStringReplaceLengthValid(t *testing.T) {
	source := stringReplaceLengthValidSource()
	for _, target := range []string{"oracle", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, code := runStringByteLength(t, target, source)
			if code != 0 || out != "replace lengths ok\n" {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
}

func TestStringReplaceLengthOverflow(t *testing.T) {
	cases := []struct{ name, input, old, replacement, call string }{
		{"replace_n_zero", `"a".repeat(2048)`, `"a"`, `"b".repeat(2097152)`, `source.replace_n(old, replacement, 2048)`},
		{"replace_zero", `"a".repeat(2048)`, `"a"`, `"b".repeat(2097152)`, `source.replace(old, replacement)`},
		{"multi_byte_needle_zero", `"aa".repeat(2048)`, `"aa"`, `"b".repeat(2097152)`, `source.replace_n(old, replacement, 2147483647)`},
		{"unicode_zero", `"é".repeat(2048)`, `"é"`, `"𝄞".repeat(524288)`, `source.replace_n(old, replacement, 2048)`},
		{"product_small", `"a".repeat(2048)`, `"a"`, `"b".repeat(2097153)`, `source.replace_n(old, replacement, 2048)`},
		{"sum_small", `"a".repeat(2048) + "xy"`, `"a"`, `"b".repeat(2097152)`, `source.replace(old, replacement)`},
		{"signed_min", `"a".repeat(1024)`, `"a"`, `"b".repeat(2097152)`, `source.replace(old, replacement)`},
		{"capped_signed_min", `"a".repeat(2048)`, `"a"`, `"b".repeat(2097152)`, `source.replace_n(old, replacement, 1024)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`import "std/string";
@noinline function count(): i32 {
  let source = %s;
  let old = %s;
  let replacement = %s;
  return %s.len();
}
function main(): i32 { print(count()); return 0; }
`, tc.input, tc.old, tc.replacement, tc.call)
			for _, target := range []string{"oracle", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, code := runStringByteLength(t, target, source)
					if code == 0 || !strings.Contains(out, "allocation size out of range") || strings.TrimSpace(withoutAbortDiag(out)) != "" {
						t.Fatalf("exit %d: %s", code, out)
					}
				})
			}
		})
	}
}
