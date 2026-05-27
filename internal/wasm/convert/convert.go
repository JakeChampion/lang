// Package convert is the Go-side mirror of
// internal/stdlib/std/wasm/convert.fern — conversion /
// reinterpret / sign-extend instruction encoders for the
// WebAssembly Core 1.0 binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/instructions.html#numeric-instructions
// (the "conversion" subsection, ops 0xA7..0xC4).
//
// Every conversion op is a single opcode byte with no immediate.
// Includes the post-MVP sign-extension ops (0xC0..0xC4) — they
// were added in the same proposal and use the same encoding
// shape so they ride along here rather than in a separate
// package. Saturating-truncate ops (multi-byte 0xFC prefix)
// aren't included; the production wasm backend doesn't use them.
package convert

// ---- Width conversions (0xA7, 0xAC, 0xAD) ----

func InstI32WrapI64(buf []byte) []byte    { return append(buf, 0xa7) }
func InstI64ExtendI32S(buf []byte) []byte { return append(buf, 0xac) }
func InstI64ExtendI32U(buf []byte) []byte { return append(buf, 0xad) }

// ---- Float -> int truncations (0xA8..0xAB, 0xAE..0xB1) ----

func InstI32TruncF32S(buf []byte) []byte { return append(buf, 0xa8) }
func InstI32TruncF32U(buf []byte) []byte { return append(buf, 0xa9) }
func InstI32TruncF64S(buf []byte) []byte { return append(buf, 0xaa) }
func InstI32TruncF64U(buf []byte) []byte { return append(buf, 0xab) }
func InstI64TruncF32S(buf []byte) []byte { return append(buf, 0xae) }
func InstI64TruncF32U(buf []byte) []byte { return append(buf, 0xaf) }
func InstI64TruncF64S(buf []byte) []byte { return append(buf, 0xb0) }
func InstI64TruncF64U(buf []byte) []byte { return append(buf, 0xb1) }

// ---- Int -> float conversions (0xB2..0xB5, 0xB7..0xBA) ----

func InstF32ConvertI32S(buf []byte) []byte { return append(buf, 0xb2) }
func InstF32ConvertI32U(buf []byte) []byte { return append(buf, 0xb3) }
func InstF32ConvertI64S(buf []byte) []byte { return append(buf, 0xb4) }
func InstF32ConvertI64U(buf []byte) []byte { return append(buf, 0xb5) }
func InstF64ConvertI32S(buf []byte) []byte { return append(buf, 0xb7) }
func InstF64ConvertI32U(buf []byte) []byte { return append(buf, 0xb8) }
func InstF64ConvertI64S(buf []byte) []byte { return append(buf, 0xb9) }
func InstF64ConvertI64U(buf []byte) []byte { return append(buf, 0xba) }

// ---- Float-width conversions (0xB6, 0xBB) ----

func InstF32DemoteF64(buf []byte) []byte  { return append(buf, 0xb6) }
func InstF64PromoteF32(buf []byte) []byte { return append(buf, 0xbb) }

// ---- Reinterpret (0xBC..0xBF) ----

func InstI32ReinterpretF32(buf []byte) []byte { return append(buf, 0xbc) }
func InstI64ReinterpretF64(buf []byte) []byte { return append(buf, 0xbd) }
func InstF32ReinterpretI32(buf []byte) []byte { return append(buf, 0xbe) }
func InstF64ReinterpretI64(buf []byte) []byte { return append(buf, 0xbf) }

// ---- Sign-extension (0xC0..0xC4, post-MVP) ----

func InstI32Extend8S(buf []byte) []byte  { return append(buf, 0xc0) }
func InstI32Extend16S(buf []byte) []byte { return append(buf, 0xc1) }
func InstI64Extend8S(buf []byte) []byte  { return append(buf, 0xc2) }
func InstI64Extend16S(buf []byte) []byte { return append(buf, 0xc3) }
func InstI64Extend32S(buf []byte) []byte { return append(buf, 0xc4) }
