package e2e

import (
	"fmt"
	"strings"
	"testing"
)

const stringByteLengthValid = `import "std/string";
function main(): i32 {
  if ("ab".repeat(3) != "ababab") { return 1; }
  if ("é𝄞".repeat(2) != "é𝄞é𝄞") { return 2; }
  if ("ab".repeat(-1) != "" || "".repeat(2147483647) != "") { return 3; }
  if ("ab".repeat_with_sep(3, ":") != "ab:ab:ab") { return 4; }
  if ("é".repeat_with_sep(3, "𝄞") != "é𝄞é𝄞é") { return 5; }
  if ("".repeat_with_sep(3, "𝄞") != "𝄞𝄞") { return 6; }
  if ("".repeat_with_sep(2147483647, "") != "") { return 7; }
  if ("x".repeat_with_sep(0, "a") != "" || "x".repeat_with_sep(-1, "a") != "") { return 8; }
  if ("x".repeat_with_sep(1, "ignored") != "x") { return 9; }
  if ("a".pad_start(3, '𝄞') != "𝄞𝄞a") { return 10; }
  if ("a".pad_end(3, '𝄞') != "a𝄞𝄞") { return 11; }
  if ("a".pad_start_str(4, "é𝄞") != "é𝄞éa") { return 12; }
  if ("a".pad_end_str(4, "é𝄞") != "aé𝄞é") { return 13; }
  if ("a".center(4, '𝄞') != "𝄞a𝄞𝄞") { return 14; }
  if ("a".pad_start(-2147483647 - 1, '𝄞') != "a") { return 15; }
  let big = "abcd".repeat(262144);
  if (big.len() != 1048576 || big[1048575] != b'd') { return 16; }
  let joined = "xy".repeat_with_sep(32769, "ab");
  if (joined.len() != 131074 || joined[131073] != b'y') { return 17; }
  print("byte lengths ok");
  return 0;
}
`

func runStringByteLength(t *testing.T, target, source string) (string, int) {
	t.Helper()
	switch target {
	case "oracle":
		return runInterpExitCode(t, source)
	case "x86-64-linux":
		return compileAndRunX86_64(t, source)
	case "arm64-linux":
		return compileAndRunArm64(t, source)
	default:
		out, stderr, code := runWasmArtifact(t, buildCLIComponent(t, source), runOpts{})
		return out + stderr, code
	}
}

func TestStringByteLengthValid(t *testing.T) {
	for _, target := range []string{"oracle", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, code := runStringByteLength(t, target, stringByteLengthValid)
			if code != 0 || out != "byte lengths ok\n" {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
}

func TestStringByteLengthOverflow(t *testing.T) {
	cases := []struct{ name, expr string }{
		{"source_product_zero", `"abcd".repeat_with_sep(1073741824, "")`},
		{"separator_product_zero", `"".repeat_with_sep(1073741825, "abcd")`},
		{"combined_product_small", `"abcd".repeat_with_sep(536870913, "abcd")`},
		{"pad_start_zero", `"a".pad_start(1073741825, '𝄞')`},
		{"pad_end_zero", `"a".pad_end(1073741825, '𝄞')`},
		{"pad_empty_zero", `"".pad_start(1073741824, '𝄞')`},
		{"pad_end_empty_zero", `"".pad_end(1073741824, '𝄞')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`import "std/string";
@noinline function count(): i32 { return %s.len(); }
function main(): i32 { print(count()); return 0; }
`, tc.expr)
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
