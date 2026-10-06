package e2ecompiler

import "testing"

func TestSelfHostStringPaddingUTF8(t *testing.T) {
	const source = `import "std/string";
import "std/utf8" as utf8;
function main(): i32 {
    if ("x".pad_start_str(4, "é") != "éx") { return 1; }
    if ("x".pad_end_str(4, "é") != "xé") { return 2; }
    if ("x".pad_start_str(3, "€") != "x") { return 3; }
    if ("x".pad_end_str(6, "aé") != "xaéa") { return 4; }
    let fills: string[] = ["", "a", "é", "€", "𐀀", "aé€𐀀"];
    for fill in fills {
        let width: i32 = 1;
        while (width < 32) {
            let left: string = "x".pad_start_str(width, fill);
            let right: string = "x".pad_end_str(width, fill);
            if (!utf8.is_valid_utf8(left) || !utf8.is_valid_utf8(right)) { return 5; }
            if (left.len() > width || right.len() > width) { return 6; }
            if (!left.ends_with("x") || !right.starts_with("x")) { return 7; }
            width = width + 1;
        }
    }
    return 0;
}
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
