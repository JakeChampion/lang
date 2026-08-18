package checker

import (
	"fmt"
	"strings"
	"testing"
)

// TestStrToOwnedAdviceIsFollowable walks the loop a reader actually walks.
//
// Storing a `str` view where an owned `string` is wanted is refused by E043 /
// E002 / E003, and all three say the same thing: "add `.to_owned()` to copy it
// into an owned string". Doing exactly that used to land on
//
//	no method "to_owned" on str (it has: as_bytes, len)
//
// which names neither the fix nor a reason to look further. The advice ran out
// one step after it was given, and the list implied `to_owned` did not exist —
// when in fact it needed `import "std/string"`, the same import its `string`
// sibling has always been told about. scalarModuleFor simply had no StrType
// case, so a `str` receiver fell past the hint to the two builtins it carries
// without the import.
//
// Each step below is the previous step's diagnostic acted on verbatim, and the
// last one must check clean. All three run through checkModuleSource, because
// the spelling the advice ends at needs an import and plain checkSource cannot
// see one — a message-text assertion alone would not have caught that the
// suggested fix was unreachable.
func TestStrToOwnedAdviceIsFollowable(t *testing.T) {
	const stored = `struct Q { tag: string }
function mk(t: string): Q { return Q { tag: %s }; }
function main(): i32 { return mk("abcdef").tag.len() - 3; }`

	err := checkModuleSource(t, fmt.Sprintf(stored, "t[0:3]"))
	if err == nil {
		t.Fatal("storing a str view in a string field should be refused")
	}
	if !strings.Contains(err.Error(), "add `.to_owned()`") {
		t.Fatalf("step 1 no longer advises .to_owned(), so this test watches the wrong loop:\n%s", err.Error())
	}

	err = checkModuleSource(t, fmt.Sprintf(stored, "t[0:3].to_owned()"))
	if err == nil {
		t.Fatal("`.to_owned()` without the import should still be refused — the import is what step 3 adds")
	}
	if want := "add `import \"std/string\"`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("step 2 leaves the reader nothing to try; wanted %q, got:\n%s", want, err.Error())
	}
	if strings.Contains(err.Error(), "it has:") {
		t.Fatalf("step 2 lists the builtins a missing import leaves behind, which points away from the fix:\n%s", err.Error())
	}

	if err := checkModuleSource(t, "import \"std/string\";\n"+fmt.Sprintf(stored, "t[0:3].to_owned()")); err != nil {
		t.Fatalf("step 3 followed both hints and still does not check:\n%v", err)
	}
}

// TestStrKeepsItsNearMissSuggestion pins the branch the import hint sits in
// front of. A near miss on a method the `str` surface really does carry is
// still answered with the name rather than with an import that would not help.
func TestStrKeepsItsNearMissSuggestion(t *testing.T) {
	err := checkModuleSource(t, `function f(t: string): i32 { return t[0:3].lenn(); }
function main(): i32 { return f("abcdef"); }`)
	if err == nil {
		t.Fatal("expected an unresolved-method error")
	}
	if !strings.Contains(err.Error(), `did you mean "len"?`) {
		t.Fatalf("a near miss on a builtin should still be named, got:\n%s", err.Error())
	}
}
