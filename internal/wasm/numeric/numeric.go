// Package numeric is the Go-side mirror of
// internal/stdlib/std/wasm/numeric.fern — arithmetic,
// comparison, and bitwise instruction encoders for the
// WebAssembly Core 1.0 binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/instructions.html#numeric-instructions
//
// Every numeric op is a single opcode byte with no immediate;
// the encoders are one-liners. Ordered by family (i32, i64,
// f32, f64) then sub-family (test/comparison/unary/binary)
// matching the spec layout. Opcode constants are in-line
// comments next to each function.
package numeric

// ---- i32 unary ----

func InstI32Clz(buf []byte) []byte    { return append(buf, 0x67) }
func InstI32Ctz(buf []byte) []byte    { return append(buf, 0x68) }
func InstI32Popcnt(buf []byte) []byte { return append(buf, 0x69) }
func InstI32Eqz(buf []byte) []byte    { return append(buf, 0x45) }

// ---- i32 comparison ----

func InstI32Eq(buf []byte) []byte   { return append(buf, 0x46) }
func InstI32Ne(buf []byte) []byte   { return append(buf, 0x47) }
func InstI32LtS(buf []byte) []byte  { return append(buf, 0x48) }
func InstI32LtU(buf []byte) []byte  { return append(buf, 0x49) }
func InstI32GtS(buf []byte) []byte  { return append(buf, 0x4a) }
func InstI32GtU(buf []byte) []byte  { return append(buf, 0x4b) }
func InstI32LeS(buf []byte) []byte  { return append(buf, 0x4c) }
func InstI32LeU(buf []byte) []byte  { return append(buf, 0x4d) }
func InstI32GeS(buf []byte) []byte  { return append(buf, 0x4e) }
func InstI32GeU(buf []byte) []byte  { return append(buf, 0x4f) }

// ---- i32 binary ----

func InstI32Add(buf []byte) []byte  { return append(buf, 0x6a) }
func InstI32Sub(buf []byte) []byte  { return append(buf, 0x6b) }
func InstI32Mul(buf []byte) []byte  { return append(buf, 0x6c) }
func InstI32DivS(buf []byte) []byte { return append(buf, 0x6d) }
func InstI32DivU(buf []byte) []byte { return append(buf, 0x6e) }
func InstI32RemS(buf []byte) []byte { return append(buf, 0x6f) }
func InstI32RemU(buf []byte) []byte { return append(buf, 0x70) }
func InstI32And(buf []byte) []byte  { return append(buf, 0x71) }
func InstI32Or(buf []byte) []byte   { return append(buf, 0x72) }
func InstI32Xor(buf []byte) []byte  { return append(buf, 0x73) }
func InstI32Shl(buf []byte) []byte  { return append(buf, 0x74) }
func InstI32ShrS(buf []byte) []byte { return append(buf, 0x75) }
func InstI32ShrU(buf []byte) []byte { return append(buf, 0x76) }
func InstI32Rotl(buf []byte) []byte { return append(buf, 0x77) }
func InstI32Rotr(buf []byte) []byte { return append(buf, 0x78) }

// ---- i64 unary ----

func InstI64Clz(buf []byte) []byte    { return append(buf, 0x79) }
func InstI64Ctz(buf []byte) []byte    { return append(buf, 0x7a) }
func InstI64Popcnt(buf []byte) []byte { return append(buf, 0x7b) }
func InstI64Eqz(buf []byte) []byte    { return append(buf, 0x50) }

// ---- i64 comparison ----

func InstI64Eq(buf []byte) []byte  { return append(buf, 0x51) }
func InstI64Ne(buf []byte) []byte  { return append(buf, 0x52) }
func InstI64LtS(buf []byte) []byte { return append(buf, 0x53) }
func InstI64LtU(buf []byte) []byte { return append(buf, 0x54) }
func InstI64GtS(buf []byte) []byte { return append(buf, 0x55) }
func InstI64GtU(buf []byte) []byte { return append(buf, 0x56) }
func InstI64LeS(buf []byte) []byte { return append(buf, 0x57) }
func InstI64LeU(buf []byte) []byte { return append(buf, 0x58) }
func InstI64GeS(buf []byte) []byte { return append(buf, 0x59) }
func InstI64GeU(buf []byte) []byte { return append(buf, 0x5a) }

// ---- i64 binary ----

func InstI64Add(buf []byte) []byte  { return append(buf, 0x7c) }
func InstI64Sub(buf []byte) []byte  { return append(buf, 0x7d) }
func InstI64Mul(buf []byte) []byte  { return append(buf, 0x7e) }
func InstI64DivS(buf []byte) []byte { return append(buf, 0x7f) }
func InstI64DivU(buf []byte) []byte { return append(buf, 0x80) }
func InstI64RemS(buf []byte) []byte { return append(buf, 0x81) }
func InstI64RemU(buf []byte) []byte { return append(buf, 0x82) }
func InstI64And(buf []byte) []byte  { return append(buf, 0x83) }
func InstI64Or(buf []byte) []byte   { return append(buf, 0x84) }
func InstI64Xor(buf []byte) []byte  { return append(buf, 0x85) }
func InstI64Shl(buf []byte) []byte  { return append(buf, 0x86) }
func InstI64ShrS(buf []byte) []byte { return append(buf, 0x87) }
func InstI64ShrU(buf []byte) []byte { return append(buf, 0x88) }
func InstI64Rotl(buf []byte) []byte { return append(buf, 0x89) }
func InstI64Rotr(buf []byte) []byte { return append(buf, 0x8a) }

// ---- f32 comparison ----

func InstF32Eq(buf []byte) []byte { return append(buf, 0x5b) }
func InstF32Ne(buf []byte) []byte { return append(buf, 0x5c) }
func InstF32Lt(buf []byte) []byte { return append(buf, 0x5d) }
func InstF32Gt(buf []byte) []byte { return append(buf, 0x5e) }
func InstF32Le(buf []byte) []byte { return append(buf, 0x5f) }
func InstF32Ge(buf []byte) []byte { return append(buf, 0x60) }

// ---- f32 unary + binary ----

func InstF32Abs(buf []byte) []byte      { return append(buf, 0x8b) }
func InstF32Neg(buf []byte) []byte      { return append(buf, 0x8c) }
func InstF32Ceil(buf []byte) []byte     { return append(buf, 0x8d) }
func InstF32Floor(buf []byte) []byte    { return append(buf, 0x8e) }
func InstF32Trunc(buf []byte) []byte    { return append(buf, 0x8f) }
func InstF32Nearest(buf []byte) []byte  { return append(buf, 0x90) }
func InstF32Sqrt(buf []byte) []byte     { return append(buf, 0x91) }
func InstF32Add(buf []byte) []byte      { return append(buf, 0x92) }
func InstF32Sub(buf []byte) []byte      { return append(buf, 0x93) }
func InstF32Mul(buf []byte) []byte      { return append(buf, 0x94) }
func InstF32Div(buf []byte) []byte      { return append(buf, 0x95) }
func InstF32Min(buf []byte) []byte      { return append(buf, 0x96) }
func InstF32Max(buf []byte) []byte      { return append(buf, 0x97) }
func InstF32Copysign(buf []byte) []byte { return append(buf, 0x98) }

// ---- f64 comparison ----

func InstF64Eq(buf []byte) []byte { return append(buf, 0x61) }
func InstF64Ne(buf []byte) []byte { return append(buf, 0x62) }
func InstF64Lt(buf []byte) []byte { return append(buf, 0x63) }
func InstF64Gt(buf []byte) []byte { return append(buf, 0x64) }
func InstF64Le(buf []byte) []byte { return append(buf, 0x65) }
func InstF64Ge(buf []byte) []byte { return append(buf, 0x66) }

// ---- f64 unary + binary ----

func InstF64Abs(buf []byte) []byte      { return append(buf, 0x99) }
func InstF64Neg(buf []byte) []byte      { return append(buf, 0x9a) }
func InstF64Ceil(buf []byte) []byte     { return append(buf, 0x9b) }
func InstF64Floor(buf []byte) []byte    { return append(buf, 0x9c) }
func InstF64Trunc(buf []byte) []byte    { return append(buf, 0x9d) }
func InstF64Nearest(buf []byte) []byte  { return append(buf, 0x9e) }
func InstF64Sqrt(buf []byte) []byte     { return append(buf, 0x9f) }
func InstF64Add(buf []byte) []byte      { return append(buf, 0xa0) }
func InstF64Sub(buf []byte) []byte      { return append(buf, 0xa1) }
func InstF64Mul(buf []byte) []byte      { return append(buf, 0xa2) }
func InstF64Div(buf []byte) []byte      { return append(buf, 0xa3) }
func InstF64Min(buf []byte) []byte      { return append(buf, 0xa4) }
func InstF64Max(buf []byte) []byte      { return append(buf, 0xa5) }
func InstF64Copysign(buf []byte) []byte { return append(buf, 0xa6) }
