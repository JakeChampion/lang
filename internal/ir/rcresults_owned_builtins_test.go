package ir

import (
	"sort"
	"strings"
	"testing"
)

// The BOX and its PAYLOAD are two answers, and the box is the outer one:
// a payload the caller owns is reachable only through a box the caller may
// release, and the release is what drops it (the enum's deep drop) at the
// sites that do not bind it. So every rcOwnedPayloadBuiltins entry must also
// be an rcOwnedResultBuiltins entry — a payload declared owned inside a box
// nobody can release is the #8405 shape, and it must not come back one
// helper at a time.
func TestOwnedPayloadBuiltinsAreOwnedResultBuiltins(t *testing.T) {
	var missing []string
	for name := range rcOwnedPayloadBuiltins {
		if !rcOwnedResultBuiltins[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d builtin(s) hand back an owned payload in a box the caller does not "+
			"own: %v — add each to rcOwnedResultBuiltins, or say why the box is not "+
			"per-call", len(missing), missing)
	}
	if len(rcOwnedPayloadBuiltins) == 0 || len(rcOwnedResultBuiltins) == 0 {
		t.Fatal("one of the two tables is empty — this gate is vacuous")
	}
}

// rcOwnedResultBuiltins is spelled the way the IR sees a CALLEE; rcResultOwned
// is mostly spelled the way the RUNTIME names its helper. Where the rename
// rule joins the two (`read_file` → `__fern_read_file`) the classifications
// have to agree, or ownedCallResultType admits a result the result axis says
// nobody owns. The only entries exempt are the method lowerings
// (`__method_Writer_write` → `__fern_writer_write`), which no table maps
// mechanically; requiring the prefix keeps a typo in a free-function name from
// passing as one of those.
func TestOwnedResultBuiltinsAgreeWithTheResultAxis(t *testing.T) {
	for name := range rcOwnedResultBuiltins {
		r, known := RcHelperResult(name)
		if known && r == RcResultOwned {
			continue
		}
		if strings.HasPrefix(name, "__method_") {
			continue
		}
		t.Errorf("%s is admitted as an owned call result but the result axis says %v "+
			"(known=%v) — a free function here must be spelled so RcHelperResult finds "+
			"it, through the rename rule or under the builtin name as `access` is", name, r, known)
	}
}

// The four terminal questions are one family, and every table here holds all
// four or none: the handle form the runtime allocates, the builtin name the
// free call is spelled with, and the `__method_Reader_` lowering. Landing them
// a table at a time is what left `window_size` admitted as a method but not as
// a free call, so a discarded `window_size(fd);` leaked the box its three
// siblings reclaimed.
func TestTerminalQuestionsAreClassifiedAsOneFamily(t *testing.T) {
	for _, q := range []string{"window_size", "set_window_size", "termios_get", "termios_set"} {
		if !rcResultOwned["__fern_handle_"+q] {
			t.Errorf("__fern_handle_%s is missing from rcResultOwned", q)
		}
		if !rcResultOwned[q] {
			t.Errorf("%s is missing from rcResultOwned", q)
		}
		if !rcOwnedResultBuiltins["__method_Reader_"+q] {
			t.Errorf("__method_Reader_%s is missing from rcOwnedResultBuiltins", q)
		}
		if !rcOwnedResultBuiltins[q] {
			t.Errorf("%s is missing from rcOwnedResultBuiltins — the free call's box "+
				"is reclaimed at no discard or argument site, though the method "+
				"spelling of the same helper is admitted", q)
		}
	}
}
