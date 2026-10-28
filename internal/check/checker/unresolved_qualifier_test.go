package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestUnimportedQualifierIsOneUnknownType(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "std/i64";
function show(r: Result[i64, number_parse.ParseNumberError]): void {
    match (r) { Ok(v) => { print(v.to_string()); }, Err(e) => { print(e.message()); } }
}
function main(): i32 {
    show(i64.parse_i64("7"));
    return 0;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("an unimported qualifier in a parameter type was accepted")
	}
	msg := err.Error()
	if strings.Count(msg, "type error at") != 1 || !strings.Contains(msg, `cannot find type "number_parse.ParseNumberError"`) {
		t.Fatalf("want one E064 and no cascade, got:\n%s", msg)
	}
}
