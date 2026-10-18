package fmtsource

import "testing"

// literateDoc has a declarations chunk, a statement-list chunk, a root that
// references chunks, a file root, a display-only snippet, a `test` example,
// a non-fern fence and prose that reads like code.
const literateDoc = "# Title\n" +
	"\n" +
	"function   prose( ):i32{return 1;}\n" +
	"<<greet>>\n" +
	"\n" +
	"```fern\n" +
	"<<greet>>=\n" +
	"// Greets.\n" +
	"function   greet( ):i32{return 7;}\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<setup>>=\n" +
	"let    x=1;\n" +
	"let y   =x+1;\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<*>>=\n" +
	"<<greet>>\n" +
	"function   main( ):i32{\n" +
	"  <<setup>>\n" +
	"  return y;\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"```fern file=lib.fern\n" +
	"pub function   lib( ):i32{return 2;}\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"function   shown( ){}\n" +
	"```\n" +
	"\n" +
	"```fern test\n" +
	"function   main( ):i32{return 0;}\n" +
	"```\n" +
	"\n" +
	"```sh\n" +
	"let    x=1;\n" +
	"```\n"

const literateWant = "# Title\n" +
	"\n" +
	"function   prose( ):i32{return 1;}\n" +
	"<<greet>>\n" +
	"\n" +
	"```fern\n" +
	"<<greet>>=\n" +
	"// Greets.\n" +
	"function greet(): i32 {\n" +
	"  return 7;\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<setup>>=\n" +
	"let x = 1;\n" +
	"let y = x + 1;\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<*>>=\n" +
	"<<greet>>\n" +
	"function   main( ):i32{\n" +
	"  <<setup>>\n" +
	"  return y;\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"```fern file=lib.fern\n" +
	"pub function lib(): i32 {\n" +
	"  return 2;\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"function   shown( ){}\n" +
	"```\n" +
	"\n" +
	"```fern test\n" +
	"function   main( ):i32{return 0;}\n" +
	"```\n" +
	"\n" +
	"```sh\n" +
	"let    x=1;\n" +
	"```\n"

// FormatLiterate formats chunk and file-root bodies, keeps prose, display
// snippets, examples, other languages and reference-bearing chunks byte for
// byte, and is a fixed point on its own output.
func TestFormatLiterate(t *testing.T) {
	got := FormatLiterate(literateDoc)
	if got != literateWant {
		t.Errorf("FormatLiterate:\n--- got ---\n%s\n--- want ---\n%s", got, literateWant)
	}
	if again := FormatLiterate(got); again != got {
		t.Errorf("FormatLiterate is not idempotent:\n%s", again)
	}
}
