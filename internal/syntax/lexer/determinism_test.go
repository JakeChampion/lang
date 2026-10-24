package lexer

import (
	"reflect"
	"testing"
)

var determinismMatrix = map[string]string{
	"minimal": `function main(): i32 { return 0; }`,

	"keywords_and_idents": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return 0 - 1; }
	while (false) { break; }
	return 0;
}`,

	"numbers_and_operators": `
function main(): i32 {
	let a: i32 = 0xDEAD_BEEF;
	let b: f64 = 3.14e-2;
	let c: i32 = 42 + (a >> 4) & 0xFF | a;
	let d: bool = a == 0 || c != 0 && a < c;
	return c;
}`,

	"strings_and_fstrings": `
function main(): i32 {
	let s: string = "hello\nworld";
	let n: i32 = 42;
	print(f"hello, {s}, value={n}, end");
	print("plain literal");
	return 0;
}`,

	"comments_and_decls": `
// top-level comment
struct Point { x: i32, y: i32 } // trailing comment
enum Shape { Circle(i32), Rect(i32, i32) }
// before fn
function main(): i32 { return 0; }`,
}

func TestTokenizeDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			firstToks, firstComments, err := Tokenize(src)
			if err != nil {
				t.Fatalf("tokenize: %v", err)
			}
			for i := 0; i < 4; i++ {
				toks, comments, err := Tokenize(src)
				if err != nil {
					t.Fatalf("tokenize run %d: %v", i+2, err)
				}
				if !reflect.DeepEqual(toks, firstToks) {
					t.Fatalf("token stream not deterministic on run %d: %d tokens vs %d tokens",
						i+2, len(toks), len(firstToks))
				}
				if !reflect.DeepEqual(comments, firstComments) {
					t.Fatalf("comment stream not deterministic on run %d: %d comments vs %d comments",
						i+2, len(comments), len(firstComments))
				}
			}
		})
	}
}
