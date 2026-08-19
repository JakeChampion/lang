package e2eselfhost

import "bytes"

// countUserCalls counts `call <callee>` sites in the self-host driver's
// emitted asm, EXCLUDING the bodies of the bundled runtime helpers (label
// prefix `__fn___fern_`). Whole-output bytes.Count breaks whenever a
// runtime helper legitimately contains the counted call in its own body:
// #4520 put a __fern_str_free release inside __fn___fern_str_arr_free, and
// #4350 slice 1 (#4551) put a fresh-fallback `call __fern_arr_box` inside
// __fn___fern_alloc_reuse — each turning exact-count assertions into a
// uniform off-by-one for EVERY program that pulls the helper in. User
// functions (`__fn_<name>`) cannot collide with the `__fern_` runtime
// namespace, so scoping by label prefix keeps the positive assertions
// meaningful while restoring the negatives.
func countUserCalls(asm []byte, callee string) int {
	needle := []byte("call " + callee)
	helperPrefix := []byte("__fn___fern_")
	count := 0
	inHelper := false
	for _, line := range bytes.Split(asm, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		// A new FUNCTION label at column 0 switches the current-function
		// context. Local labels (`.Lwhile_3:` etc.) also sit at column 0
		// inside a body — they start with '.' and must not reset it.
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '.' && bytes.HasSuffix(trimmed, []byte(":")) {
			inHelper = bytes.HasPrefix(trimmed, helperPrefix)
			continue
		}
		if !inHelper && bytes.Contains(line, needle) {
			count++
		}
	}
	return count
}

// countCallsInFn counts `call <callee>` sites inside ONE user function
// `__fn_<fn>`, for a contract about where a release lands rather than whether
// the program contains one at all. A caller-side reclaim and a callee-side one
// are different verdicts, and a whole-output count cannot tell them apart.
func countCallsInFn(asm []byte, fn string, callee string) int {
	needle := []byte("call " + callee)
	label := []byte("__fn_" + fn + ":")
	count := 0
	inFn := false
	for _, line := range bytes.Split(asm, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '.' && bytes.HasSuffix(trimmed, []byte(":")) {
			inFn = bytes.Equal(trimmed, label)
			continue
		}
		if inFn && bytes.Contains(line, needle) {
			count++
		}
	}
	return count
}

// countUserStrFreeReclaims counts user-code `call __fn___fern_str_free`
// sites (see countUserCalls for the helper-body exclusion rationale).
func countUserStrFreeReclaims(asm []byte) int {
	return countUserCalls(asm, "__fn___fern_str_free")
}

// countUserArrBoxAllocs counts user-code `call __fern_arr_box` sites —
// the box-allocation figure the reuse emission-contract tests pin.
// __fn___fern_alloc_reuse's fresh-fallback arm (#4551) is runtime
// machinery, not a construction site, and is excluded with the rest of
// the helper bodies.
func countUserArrBoxAllocs(asm []byte) int {
	return countUserCalls(asm, "__fern_arr_box")
}
