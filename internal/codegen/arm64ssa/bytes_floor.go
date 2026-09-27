package arm64ssa

// The bytes floor (#9853): the two provided callees that hand a Fern
// runtime body a string's or a byte array's storage, so the socket send and
// receive helpers are Fern bodies rather than assembly.

// emitStrBytesHelper writes __str_bytes(s, scratch) -> data: a string on
// this backend is always its heap data pointer (nothing here builds the
// inline form the stack backend's SSO seam spills), so x0 is already the
// answer and x1 is not read. Leaf.
func emitStrBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_bytes"))
	w("\tret")
}

// emitArrSetLenHelper writes __arr_set_len(data, n): the length word at
// data-4 of an __alloc_u8 box becomes n; the capacity at data-12 stays.
// Leaf.
func emitArrSetLenHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__arr_set_len"))
	w("\tstur w1, [x0, #-4]")
	w("\tret")
}
