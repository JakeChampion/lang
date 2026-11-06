package checker

import (
	"strings"
	"testing"
)

func TestMainSignatureSkipsMethods(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"main.fern": "function (p: Nope) main(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n",
	}, "main.fern")
	if err == nil {
		t.Fatal("a method on an unknown receiver type checked clean, want E021")
	}
	if !strings.Contains(err.Error(), "method receiver type") {
		t.Errorf("error %q is not the E021 receiver refusal", err)
	}
	if strings.Contains(err.Error(), "main takes no parameters") {
		t.Errorf("error %q reports E084 for a method", err)
	}
}

// A main of an imported module has the signature of an entry, as the
// entry's main does (#14286).
func TestMainSignatureInAnImportedModule(t *testing.T) {
	for _, tc := range []struct{ name, lib, want string }{
		{"params", "function main(n: i32): void {}\npub function f(): i32 { return 1; }\n", "main takes no parameters"},
		{"result", "function main(): string { return \"x\"; }\npub function f(): i32 { return 1; }\n", "main returns an integer, a boolean or void, not string"},
		{"valid", "function main(): i32 { return 0; }\npub function f(): i32 { return 1; }\n", ""},
		{"other-name", "function mainly(n: i32): i32 { return n; }\npub function f(): i32 { return 1; }\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, _ := checkFiles(t, map[string]string{
				"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.f(); }\n",
				"lib.fern":  tc.lib,
			}, "main.fern")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error with %q, got %v", tc.want, err)
			}
		})
	}
}
