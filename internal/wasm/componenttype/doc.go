// Regeneration notes for lang.bin / http.bin.
//
// The two .bin files hold the inner *payload* of the
// `component-type` custom section that wasm-tools writes when
// you run `wasm-tools component embed -w <world>` against the
// WIT files in cmd/lang/wit/. They are world-specific and
// module-independent: the same payload is emitted regardless
// of the core module it is being attached to.
//
// To regenerate from a fresh wit/ tree:
//
//	# 1. Build an empty core module (the payload doesn't read it)
//	echo '(module)' > /tmp/empty.wat
//	wasm-tools parse /tmp/empty.wat -o /tmp/empty.wasm
//
//	# 2. Embed each world into the empty module
//	wasm-tools component embed cmd/lang/wit -w lang \
//	    /tmp/empty.wasm -o /tmp/lang.wasm
//	wasm-tools component embed cmd/lang/wit -w http \
//	    /tmp/empty.wasm -o /tmp/http.wasm
//
//	# 3. Strip everything before the custom-section payload:
//	#    8 (module header) + 1 (custom section id) + 2 (size uleb)
//	#    + 1 (name length uleb) + 14 (name "component-type") = 26 bytes.
//	#    (If a section size needs 3+ uleb bytes for a future world,
//	#    re-derive this offset from `wasm-tools dump`.)
//	dd if=/tmp/lang.wasm of=internal/wasm/componenttype/lang.bin bs=1 skip=26
//	dd if=/tmp/http.wasm of=internal/wasm/componenttype/http.bin bs=1 skip=26
//
// Anything that changes the WIT (adding/removing imports,
// version bumps) requires regenerating both files. The CI
// equivalence test (componenttype_test.go) compares our output
// to a live wasm-tools embed and catches drift if regeneration
// is skipped.
package componenttype
