package checker

import (
	"fmt"
	"strings"
	"testing"
)

func TestStrToOwnedAdviceIsFollowable(t *testing.T) {
	const stored = `struct Q { tag: string }
function mk(t: string): Q { return Q { tag: %s }; }
function main(): i32 { return mk("abcdef").tag.len() - 3; }`

	err := checkModuleSource(t, fmt.Sprintf(stored, "slice_unchecked(t, 0, 3)"))
	if err == nil {
		t.Fatal("storing a str view in a string field should be refused")
	}
	if !strings.Contains(err.Error(), "add `.to_owned()`") {
		t.Fatalf("step 1 no longer advises .to_owned(), so this test watches the wrong loop:\n%s", err.Error())
	}

	err = checkModuleSource(t, fmt.Sprintf(stored, "slice_unchecked(t, 0, 3).to_owned()"))
	if err == nil {
		t.Fatal("`.to_owned()` without the import should still be refused — the import is what step 3 adds")
	}
	if want := "add `import \"std/string\"`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("step 2 leaves the reader nothing to try; wanted %q, got:\n%s", want, err.Error())
	}
	if strings.Contains(err.Error(), "it has:") {
		t.Fatalf("step 2 lists the builtins a missing import leaves behind, which points away from the fix:\n%s", err.Error())
	}

	if err := checkModuleSource(t, "import \"std/string\";\n"+fmt.Sprintf(stored, "slice_unchecked(t, 0, 3).to_owned()")); err != nil {
		t.Fatalf("step 3 followed both hints and still does not check:\n%v", err)
	}
}

func TestStrKeepsItsNearMissSuggestion(t *testing.T) {
	err := checkModuleSource(t, `function f(t: string): i32 { return slice_unchecked(t, 0, 3).lenn(); }
function main(): i32 { return f("abcdef"); }`)
	if err == nil {
		t.Fatal("expected an unresolved-method error")
	}
	if !strings.Contains(err.Error(), `did you mean "len"?`) {
		t.Fatalf("a near miss on a builtin should still be named, got:\n%s", err.Error())
	}
}
