package fmtsource

import "testing"

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

func TestFormatLiterate(t *testing.T) {
	got := FormatLiterate(literateDoc)
	if got != literateWant {
		t.Errorf("FormatLiterate:\n--- got ---\n%s\n--- want ---\n%s", got, literateWant)
	}
	if again := FormatLiterate(got); again != got {
		t.Errorf("FormatLiterate is not idempotent:\n%s", again)
	}
}
