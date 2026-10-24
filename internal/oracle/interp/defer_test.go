package interp

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func evalProgramCapture(t *testing.T, src string) (Value, string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, ok := i.Funcs["main"]; !ok {
		t.Fatalf("program has no main")
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	return v, buf.String()
}

func TestInterpDeferRunsAtExit(t *testing.T) {
	_, out := evalProgramCapture(t, `function main(): void {
		defer print("cleanup");
		print("body");
	}`)
	if out != "body\ncleanup\n" {
		t.Errorf("defer-at-exit: stdout = %q, want \"body\\ncleanup\\n\"", out)
	}
}

func TestInterpDeferLIFO(t *testing.T) {
	_, out := evalProgramCapture(t, `function main(): void {
		defer print("a");
		defer print("b");
		defer print("c");
		print("body");
	}`)
	if out != "body\nc\nb\na\n" {
		t.Errorf("defer LIFO: stdout = %q, want \"body\\nc\\nb\\na\\n\"", out)
	}
}

func TestInterpDeferUnreachedIsNoop(t *testing.T) {
	_, out := evalProgramCapture(t, `function main(): void {
		if (false) { defer print("never"); }
		print("body");
	}`)
	if out != "body\n" {
		t.Errorf("unreached defer: stdout = %q, want \"body\\n\" (defer must not fire)", out)
	}
}

func TestInterpDeferOnEarlyReturn(t *testing.T) {
	v, out := evalProgramCapture(t, `function main(): i32 {
		defer print("cleanup");
		if (true) { return 1; }
		print("unreached");
		return 0;
	}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Errorf("early return: got %v, want 1", v)
	}
	if out != "cleanup\n" {
		t.Errorf("defer on early return: stdout = %q, want \"cleanup\\n\"", out)
	}
}

func TestInterpDeferReturnValueComputedFirst(t *testing.T) {
	v, _ := evalProgramCapture(t, `function set0(c: Cell[i32]): void { c.set(99); }
	function helper(): i32 {
		let c: Cell[i32] = cell_new(5);
		defer set0(c);
		return c.get();
	}
	function main(): i32 {
		return helper();
	}`)
	if n, ok := v.(Number); !ok || n != 5 {
		t.Errorf("return-before-defer: got %v, want 5 (defer mutation must not affect return value)", v)
	}
}
