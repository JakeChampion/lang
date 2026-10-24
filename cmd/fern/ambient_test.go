package main

import (
	"strings"
	"testing"
)

func TestCheckRefusesAmbientEffectInHandler(t *testing.T) {
	entry := writeFern(t, `import "std/http";
import "std/platform";
function helper(): void { eprint("hit"); }
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    helper();
    return http.ok("");
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

func TestCheckAllowsEffectsThroughTheBag(t *testing.T) {
	entry := writeFern(t, `import "std/http";
import "std/platform";
function helper(plat: platform.Platform): void { plat.log("hit"); }
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    helper(plat);
    return http.ok("");
}
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err != nil {
		t.Fatalf("a handler using its bag rejected: %v", err)
	}
}
