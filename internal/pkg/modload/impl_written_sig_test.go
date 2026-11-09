package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/printer"
)

// The formatter prints an impl method's signature as written, so the
// loader must mangle that copy as well as the resolved one.
func TestImplMethodWrittenSignatureIsMangled(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.fern": `import "core/cmp";
pub struct P { x: i32 }
pub function zero(): P { return P { x: 0 }; }
impl cmp.Default for P {
    function default(): P { return zero(); }
}
impl cmp.Debug for P {
    function to_debug(self: Self): string { return "p"; }
}
impl cmp.Eq for P {
    function eq(self: P, other: P): boolean { return self.x == other.x; }
}`,
		"main.fern": `import "./lib";
function main(): i32 { return lib.zero().x; }`,
	})
	out := printer.Format(loadProg(t, filepath.Join(dir, "main.fern")))
	for _, want := range []string{
		"function default(): lib__P {",
		"function to_debug(self: Self): string {",
		"function eq(self: lib__P, other: lib__P): boolean {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("merged program lacks %q:\n%s", want, out)
		}
	}
}
