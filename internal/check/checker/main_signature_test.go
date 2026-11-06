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

// The main of an imported module has the signature of an entry too, and the
// error names the file of that module (#14286).
func TestMainSignatureInImportedModule(t *testing.T) {
	err, paths := checkFiles(t, map[string]string{
		"lib.fern":  "function main(n: i32): void {}\npub function f(): i32 { return 1; }\n",
		"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.f() - 1; }\n",
	}, "main.fern")
	if err == nil || !strings.Contains(err.Error(), "main takes no parameters") {
		t.Fatalf("the main of lib.fern takes a parameter: got %v, want E084", err)
	}
	if files := filesOf(t, err); len(files) != 1 || files[0] != paths["lib.fern"] {
		t.Fatalf("E084 names %v, want %s", files, paths["lib.fern"])
	}
	err, _ = checkFiles(t, map[string]string{
		"lib.fern":  "function main(): i32 { return 0; }\npub function f(): i32 { return 1; }\n",
		"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.f() - 1; }\n",
	}, "main.fern")
	if err != nil {
		t.Fatalf("a module with a valid main: %v", err)
	}
}
