package main

import (
	"strings"
	"testing"
)

// The ambient-effect rule (E080) is target-independent, so a bare `-check`
// runs it: a handler reaching a host effect around its bag is refused with
// the chain that reaches it and the bag method to call instead.
func TestCheckRefusesAmbientEffectInHandler(t *testing.T) {
	entry := writeFern(t, `import "std/http";
function helper(): void { eprint("hit"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    helper();
    return http.http_response_ok("");
}
function main(): i32 { return 0; }
`)
	err := runCheck(entry, "")
	if err == nil {
		t.Fatal("a handler reaching eprint through a helper should be E080")
	}
	got := err.Error()
	for _, want := range []string{"E080", "handle -> helper -> eprint", "plat.log("} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q:\n%s", want, got)
		}
	}
}

// The complement: the same effect through the bag checks clean, so the rule
// sends a handler somewhere it can go.
func TestCheckAllowsEffectsThroughTheBag(t *testing.T) {
	entry := writeFern(t, `import "std/http";
import "std/platform";
function helper(plat: Platform): void { plat.log("hit"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    helper(plat);
    return http.http_response_ok("");
}
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err != nil {
		t.Fatalf("a handler using its bag rejected: %v", err)
	}
}
