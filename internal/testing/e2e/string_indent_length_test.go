package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func stringIndentLengthValidSource() string {
	var source strings.Builder
	source.WriteString(`import "std/string";
@noinline function indented(text: string, prefix: string): string {
  return text.repeat(2).indent(prefix.repeat(2));
}
@noinline function check(text: string, prefix: string, want: string): boolean {
  let result = indented(text, prefix);
  let churn = "heap churn".repeat(32);
  return churn.len() == 320 && result == want;
}
function main(): i32 {
`)
	index := 0
	for _, text := range []string{"", "x", "\n", "\n\n", "x\n", "x\ny", "\nx", "\r", "\r\n", "a\r\nb\n", "é\n𝄞\n🙂", "\x00\nx"} {
		for _, prefix := range []string{"", "> ", "\n", "\r\n", "𝄞\x00"} {
			var want strings.Builder
			for _, line := range strings.SplitAfter(text+text, "\n") {
				if line != "" {
					want.WriteString(prefix + prefix)
					want.WriteString(line)
				}
			}
			index++
			fmt.Fprintf(&source, "  if (!check(%s, %s, %s)) { return %d; }\n", strconv.Quote(text), strconv.Quote(prefix), strconv.Quote(want.String()), index)
		}
	}
	source.WriteString(`  let huge = "p".repeat(2097152);
  if ("".indent(huge) != "") { return 61; }
  let one = "x".indent(huge);
  if (one.len() != 2097153 || one[0] != b'p' || one[2097152] != b'x') { return 62; }
  let lines = "\n".repeat(1024).indent("p".repeat(1024));
  if (lines.len() != 1049600 || lines[1024] != 10 || lines[1049599] != 10) { return 63; }
  let tail = "\nx".repeat(512).indent("p".repeat(1024));
  if (tail.len() != 526336 || tail[526335] != b'x') { return 64; }
  print("indent lengths ok");
  return 0;
}
`)
	return source.String()
}

func stringIndentLengthOverflowSources() []struct{ name, source string } {
	cases := []struct{ name, text, prefix string }{
		{"product_wrap", `"\n".repeat(2048)`, `"p".repeat(2097152)`},
		{"product_small", `"\n".repeat(2048)`, `"p".repeat(2097153)`},
		{"signed_min", `"\n".repeat(1024)`, `"p".repeat(2097152)`},
		{"no_final_newline", `"\nx".repeat(2047) + "x"`, `"p".repeat(2097152)`},
		{"unicode_source", `"é\n".repeat(2048)`, `"p".repeat(2097152)`},
		{"unicode_prefix", `"\n".repeat(2048)`, `"𝄞".repeat(524288)`},
	}
	result := []struct{ name, source string }{}
	for _, tc := range cases {
		source := fmt.Sprintf(`import "std/string";
@noinline function count(): i32 {
  let text = %s;
  let prefix = %s;
  return text.indent(prefix).len();
}
function main(): i32 { print(count()); return 0; }
`, tc.text, tc.prefix)
		result = append(result, struct{ name, source string }{tc.name, source})
	}
	return result
}

func TestStringIndentLengthValid(t *testing.T) {
	for _, target := range []string{"oracle", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, code := runStringByteLength(t, target, stringIndentLengthValidSource())
			if code != 0 || out != "indent lengths ok\n" {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
}

func TestStringIndentLengthOverflow(t *testing.T) {
	for _, tc := range stringIndentLengthOverflowSources() {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"oracle", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					var out string
					var code int
					if target == "oracle" {
						out, code = runInterpExitCode(t, tc.source)
					} else {
						out, code = runArrayJoinLengthBounded(t, target, tc.source)
					}
					if code == 0 || !strings.Contains(out, "allocation size out of range") || strings.TrimSpace(withoutAbortDiag(out)) != "" {
						t.Fatalf("exit %d: %s", code, out)
					}
				})
			}
		})
	}
}
