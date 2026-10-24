package literate

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func tangle(t *testing.T, src string) (string, []Line) {
	t.Helper()
	code, lm, err := Parse(src).Tangle()
	if err != nil {
		t.Fatalf("Tangle: %v", err)
	}
	return code, lm
}

func TestTangleRootOnly(t *testing.T) {
	src := "# Title\n\nProse.\n\n```fern\n<<*>>=\nfn main() {}\n```\n"
	code, _ := tangle(t, src)
	if code != "fn main() {}" {
		t.Errorf("tangled = %q, want %q", code, "fn main() {}")
	}
}

func TestTangleOutOfOrderReference(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<*>>=",
		"<<greeting>>",
		"fn main() { print(greeting()); }",
		"```",
		"",
		"The helper is defined only now, below its use:",
		"",
		"```fern",
		"<<greeting>>=",
		`fn greeting(): string { "hi" }`,
		"```",
	}, "\n")
	code, _ := tangle(t, src)
	want := "fn greeting(): string { \"hi\" }\nfn main() { print(greeting()); }"
	if code != want {
		t.Errorf("tangled =\n%s\n\nwant\n%s", code, want)
	}
}

func TestTangleConcatenatesPieces(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<*>>=",
		"<<body>>",
		"```",
		"```fern",
		"<<body>>=",
		"line one",
		"```",
		"```fern",
		"<<body>>=",
		"line two",
		"```",
	}, "\n")
	code, _ := tangle(t, src)
	if code != "line one\nline two" {
		t.Errorf("tangled = %q, want %q", code, "line one\nline two")
	}
}

func TestTangleIndentationAndLineMap(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<*>>=",
		"fn main() {",
		"    <<body>>",
		"}",
		"```",
		"```fern",
		"<<body>>=",
		"return 7;",
		"```",
	}, "\n")
	code, lm := tangle(t, src)
	want := "fn main() {\n    return 7;\n}"
	if code != want {
		t.Errorf("tangled =\n%s\n\nwant\n%s", code, want)
	}
	if len(lm) != 3 {
		t.Fatalf("line map len = %d, want 3", len(lm))
	}
	if lm[1].Lit != 9 || lm[1].ColShift != 4 {
		t.Errorf("lineMap[1] = %+v, want {Lit:9 ColShift:4}", lm[1])
	}
	if lm[0].Lit != 3 || lm[0].ColShift != 0 {
		t.Errorf("lineMap[0] = %+v, want {Lit:3 ColShift:0}", lm[0])
	}
}

func TestTangleDisplayOnlyExcluded(t *testing.T) {
	src := strings.Join([]string{
		"Here's an illustration that should NOT compile in:",
		"",
		"```fern",
		"this is not real code, just an example",
		"```",
		"",
		"```fern",
		"<<*>>=",
		"fn main() {}",
		"```",
	}, "\n")
	code, _ := tangle(t, src)
	if code != "fn main() {}" {
		t.Errorf("tangled = %q, want only the root chunk", code)
	}
	if strings.Contains(code, "illustration") || strings.Contains(code, "not real code") {
		t.Errorf("display-only block leaked into tangled output: %q", code)
	}
}

func TestTangleMissingRoot(t *testing.T) {
	_, _, err := Parse("```fern\n<<helper>>=\nfn h() {}\n```\n").Tangle()
	if err == nil {
		t.Fatal("expected error for missing root chunk")
	}
	if !strings.Contains(err.Error(), "no root chunk") {
		t.Errorf("error = %q, want it to mention the missing root chunk", err.Error())
	}
}

func TestTangleUndefinedReference(t *testing.T) {
	src := "```fern\n<<*>>=\n<<nope>>\n```\n"
	_, _, err := Parse(src).Tangle()
	if err == nil {
		t.Fatal("expected error for undefined chunk reference")
	}
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *literate.Error", err)
	}
	if !strings.Contains(pe.Error(), "undefined chunk") || !strings.Contains(pe.Error(), "nope") {
		t.Errorf("error = %q, want it to name the undefined chunk", pe.Error())
	}
	if pe.Position() != (ast.Position{Line: 3, Col: 1}) {
		t.Errorf("position = %v, want 3:1", pe.Position())
	}
}

func TestTangleCyclicReference(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<*>>=",
		"<<a>>",
		"```",
		"```fern",
		"<<a>>=",
		"<<b>>",
		"```",
		"```fern",
		"<<b>>=",
		"<<a>>",
		"```",
	}, "\n")
	_, _, err := Parse(src).Tangle()
	if err == nil {
		t.Fatal("expected error for cyclic chunk reference")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Errorf("error = %q, want it to mention a cycle", err.Error())
	}
}

func TestTangleFenceVariants(t *testing.T) {
	src := strings.Join([]string{
		"````fern",
		"",
		"<<*>>=",
		"fn main() {}",
		"````",
	}, "\n")
	code, _ := tangle(t, src)
	if code != "fn main() {}" {
		t.Errorf("tangled = %q, want %q", code, "fn main() {}")
	}
}
