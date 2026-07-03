package e2eselfhost

import "bytes"

// countUserStrFreeReclaims counts `call __fn___fern_str_free` sites in the
// self-host driver's emitted asm, EXCLUDING the bodies of the bundled
// runtime helpers (label prefix `__fn___fern_`). Since #4520 the
// `__fn___fern_str_arr_free` helper legitimately releases each element via
// __fern_str_free, so a whole-output bytes.Count reads ≥1 for EVERY program
// that pulls the helper in — turning the "expected NO reclaim" negative
// assertions in the str-reclaim test family into false positives. User
// functions (`__fn_<name>`) cannot collide with the `__fern_` runtime
// namespace, so scoping by label prefix keeps every positive assertion
// meaningful while restoring the negatives.
func countUserStrFreeReclaims(asm []byte) int {
	needle := []byte("call __fn___fern_str_free")
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
