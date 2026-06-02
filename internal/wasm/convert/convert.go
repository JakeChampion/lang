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
// package. The saturating-truncate ops (the multi-byte 0xFC
// prefix, from the nontrapping-float-to-int-conversions proposal)
// are included below: the production wasm backend uses them so a
// float→int cast saturates (NaN → 0, out-of-range → INT_MIN /
// INT_MAX) instead of trapping, matching the other backends.
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

// ---- Saturating float -> int truncations (0xFC prefix, subops 0..7) ----
//
// The 0xFC-prefixed encoding is the opcode byte followed by a
// ULEB128 subopcode; subops 0..7 fit in a single byte. Unlike the
// 0xA8..0xB1 family these never trap: NaN converts to 0 and an
// out-of-range value clamps to the destination's INT_MIN / INT_MAX
// (or 0 / UINT_MAX for the unsigned variants).

func InstI32TruncSatF32S(buf []byte) []byte { return append(buf, 0xfc, 0x00) }
func InstI32TruncSatF32U(buf []byte) []byte { return append(buf, 0xfc, 0x01) }
func InstI32TruncSatF64S(buf []byte) []byte { return append(buf, 0xfc, 0x02) }
func InstI32TruncSatF64U(buf []byte) []byte { return append(buf, 0xfc, 0x03) }
func InstI64TruncSatF32S(buf []byte) []byte { return append(buf, 0xfc, 0x04) }
func InstI64TruncSatF32U(buf []byte) []byte { return append(buf, 0xfc, 0x05) }
func InstI64TruncSatF64S(buf []byte) []byte { return append(buf, 0xfc, 0x06) }
func InstI64TruncSatF64U(buf []byte) []byte { return append(buf, 0xfc, 0x07) }

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
