// Command unicodegen generates internal/stdlib/std/unicode.fern — the
// Unicode case-mapping tables (the simple 1:1 runs plus the
// unconditional full 1→N mappings), the character-class range tables,
// and the API built on them. The data comes from the Go standard
// library's `unicode` package (unicode.ToUpper / unicode.ToLower and the
// category predicates), so the tables' Unicode version tracks the Go
// toolchain used to run this generator.
//
// Usage:
//
//	go run ./cmd/unicodegen internal/stdlib/std/unicode.fern
//
// Re-run after a Go toolchain upgrade to refresh the tables; the output
// is deterministic (sorted by code point), so an unchanged Unicode
// version reproduces the file byte-for-byte.
//
// # Why the tables are strings
//
// The tables were once emitted as `function _upper_keys(): i32[] { return
// [97, 98, …] }` — 2900 array-literal stores of *code*, materialised on
// every single call. That cost 176 KB of binary and ran 22x slower than
// the ASCII byte fold on ASCII input (#5627). A string literal is static
// rodata: it costs its own length, once, and indexing it allocates
// nothing. So every table here is a string in a fixed-width, 6-bit
// character encoding, binary-searched in place.
//
// The alphabet is 64 printable ASCII characters in two contiguous spans,
// 48..91 ('0'..'[') and 93..112 (']'..'p'), chosen to skip `\` (92) so no
// emitted literal ever needs an escape — `"` (34) is below the range, and
// a `\xNN` escape for a byte >= 0x80 would make the literal invalid
// UTF-8. Decoding is two comparisons (see `_dig` in the generated file).
//
// A field is 4 characters = 24 bits. Deltas are stored biased by 2^23 so
// they can be negative.
//
// # Record layouts
//
// Case table — 5 fields (20 chars) per entry, sorted by lo, disjoint:
//
//	lo | hi | kind | dUpper | dLower
//
//	kind 0: constant deltas. ToUpper(cp) = cp + dUpper, likewise lower.
//	kind 1: alternating pair run — even offset from lo is UPPERCASE, odd
//	        is its lowercase (the Latin-Extended-A shape: Ā ā Ă ă …).
//
// Class tables — 2 fields (8 chars) per entry: lo | hi, inclusive.
//
// Correctness does not rest on the run-derivation being clever: `verify`
// below decodes the emitted tables for every code point in 0..MaxRune and
// compares against the `unicode` package, failing the build on any
// mismatch.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// bias is added to a stored delta so negative deltas fit an unsigned
// 24-bit field.
const bias = 1 << 23

// fullCase is one entry of the UNCONDITIONAL full case mapping: a code
// point whose full mapping is more than one code point, so the simple
// (1:1) table cannot express it. `to` is zero-padded on the right.
type fullCase struct {
	from rune
	to   [3]rune
}

// fullUpper / fullLower are the unconditional section of the Unicode
// SpecialCasing data, transcribed here because Go's `unicode` package
// has no full mappings at all — it does simple mapping only, and Go's
// own answer for full mapping lives outside the standard library in
// x/text/cases, which this zero-dependency module does not have. So
// unlike every other table in this generator, these are NOT derived
// from an oracle at build time; they are data, pinned by
// TestFullCaseKnownAnswers and by the e2e suite.
//
// 102 uppercase entries and exactly 1 lowercase (U+0130), matching
// SpecialCasing.txt's unconditional section, which has been stable for
// many Unicode versions. Greek Final_Sigma is handled separately, as a
// context rule over the Cased / Case_Ignorable tables rather than a table
// entry (see isCased / isCaseIgnorable). The Lithuanian and Turkish
// tailorings are out of scope by design.

var fullUpper = []fullCase{
	{0x00DF, [3]rune{0x0053, 0x0053, 0x0000}},
	{0x0149, [3]rune{0x02BC, 0x004E, 0x0000}},
	{0x01F0, [3]rune{0x004A, 0x030C, 0x0000}},
	{0x0390, [3]rune{0x0399, 0x0308, 0x0301}},
	{0x03B0, [3]rune{0x03A5, 0x0308, 0x0301}},
	{0x0587, [3]rune{0x0535, 0x0552, 0x0000}},
	{0x1E96, [3]rune{0x0048, 0x0331, 0x0000}},
	{0x1E97, [3]rune{0x0054, 0x0308, 0x0000}},
	{0x1E98, [3]rune{0x0057, 0x030A, 0x0000}},
	{0x1E99, [3]rune{0x0059, 0x030A, 0x0000}},
	{0x1E9A, [3]rune{0x0041, 0x02BE, 0x0000}},
	{0x1F50, [3]rune{0x03A5, 0x0313, 0x0000}},
	{0x1F52, [3]rune{0x03A5, 0x0313, 0x0300}},
	{0x1F54, [3]rune{0x03A5, 0x0313, 0x0301}},
	{0x1F56, [3]rune{0x03A5, 0x0313, 0x0342}},
	{0x1F80, [3]rune{0x1F08, 0x0399, 0x0000}},
	{0x1F81, [3]rune{0x1F09, 0x0399, 0x0000}},
	{0x1F82, [3]rune{0x1F0A, 0x0399, 0x0000}},
	{0x1F83, [3]rune{0x1F0B, 0x0399, 0x0000}},
	{0x1F84, [3]rune{0x1F0C, 0x0399, 0x0000}},
	{0x1F85, [3]rune{0x1F0D, 0x0399, 0x0000}},
	{0x1F86, [3]rune{0x1F0E, 0x0399, 0x0000}},
	{0x1F87, [3]rune{0x1F0F, 0x0399, 0x0000}},
	{0x1F88, [3]rune{0x1F08, 0x0399, 0x0000}},
	{0x1F89, [3]rune{0x1F09, 0x0399, 0x0000}},
	{0x1F8A, [3]rune{0x1F0A, 0x0399, 0x0000}},
	{0x1F8B, [3]rune{0x1F0B, 0x0399, 0x0000}},
	{0x1F8C, [3]rune{0x1F0C, 0x0399, 0x0000}},
	{0x1F8D, [3]rune{0x1F0D, 0x0399, 0x0000}},
	{0x1F8E, [3]rune{0x1F0E, 0x0399, 0x0000}},
	{0x1F8F, [3]rune{0x1F0F, 0x0399, 0x0000}},
	{0x1F90, [3]rune{0x1F28, 0x0399, 0x0000}},
	{0x1F91, [3]rune{0x1F29, 0x0399, 0x0000}},
	{0x1F92, [3]rune{0x1F2A, 0x0399, 0x0000}},
	{0x1F93, [3]rune{0x1F2B, 0x0399, 0x0000}},
	{0x1F94, [3]rune{0x1F2C, 0x0399, 0x0000}},
	{0x1F95, [3]rune{0x1F2D, 0x0399, 0x0000}},
	{0x1F96, [3]rune{0x1F2E, 0x0399, 0x0000}},
	{0x1F97, [3]rune{0x1F2F, 0x0399, 0x0000}},
	{0x1F98, [3]rune{0x1F28, 0x0399, 0x0000}},
	{0x1F99, [3]rune{0x1F29, 0x0399, 0x0000}},
	{0x1F9A, [3]rune{0x1F2A, 0x0399, 0x0000}},
	{0x1F9B, [3]rune{0x1F2B, 0x0399, 0x0000}},
	{0x1F9C, [3]rune{0x1F2C, 0x0399, 0x0000}},
	{0x1F9D, [3]rune{0x1F2D, 0x0399, 0x0000}},
	{0x1F9E, [3]rune{0x1F2E, 0x0399, 0x0000}},
	{0x1F9F, [3]rune{0x1F2F, 0x0399, 0x0000}},
	{0x1FA0, [3]rune{0x1F68, 0x0399, 0x0000}},
	{0x1FA1, [3]rune{0x1F69, 0x0399, 0x0000}},
	{0x1FA2, [3]rune{0x1F6A, 0x0399, 0x0000}},
	{0x1FA3, [3]rune{0x1F6B, 0x0399, 0x0000}},
	{0x1FA4, [3]rune{0x1F6C, 0x0399, 0x0000}},
	{0x1FA5, [3]rune{0x1F6D, 0x0399, 0x0000}},
	{0x1FA6, [3]rune{0x1F6E, 0x0399, 0x0000}},
	{0x1FA7, [3]rune{0x1F6F, 0x0399, 0x0000}},
	{0x1FA8, [3]rune{0x1F68, 0x0399, 0x0000}},
	{0x1FA9, [3]rune{0x1F69, 0x0399, 0x0000}},
	{0x1FAA, [3]rune{0x1F6A, 0x0399, 0x0000}},
	{0x1FAB, [3]rune{0x1F6B, 0x0399, 0x0000}},
	{0x1FAC, [3]rune{0x1F6C, 0x0399, 0x0000}},
	{0x1FAD, [3]rune{0x1F6D, 0x0399, 0x0000}},
	{0x1FAE, [3]rune{0x1F6E, 0x0399, 0x0000}},
	{0x1FAF, [3]rune{0x1F6F, 0x0399, 0x0000}},
	{0x1FB2, [3]rune{0x1FBA, 0x0399, 0x0000}},
	{0x1FB3, [3]rune{0x0391, 0x0399, 0x0000}},
	{0x1FB4, [3]rune{0x0386, 0x0399, 0x0000}},
	{0x1FB6, [3]rune{0x0391, 0x0342, 0x0000}},
	{0x1FB7, [3]rune{0x0391, 0x0342, 0x0399}},
	{0x1FBC, [3]rune{0x0391, 0x0399, 0x0000}},
	{0x1FC2, [3]rune{0x1FCA, 0x0399, 0x0000}},
	{0x1FC3, [3]rune{0x0397, 0x0399, 0x0000}},
	{0x1FC4, [3]rune{0x0389, 0x0399, 0x0000}},
	{0x1FC6, [3]rune{0x0397, 0x0342, 0x0000}},
	{0x1FC7, [3]rune{0x0397, 0x0342, 0x0399}},
	{0x1FCC, [3]rune{0x0397, 0x0399, 0x0000}},
	{0x1FD2, [3]rune{0x0399, 0x0308, 0x0300}},
	{0x1FD3, [3]rune{0x0399, 0x0308, 0x0301}},
	{0x1FD6, [3]rune{0x0399, 0x0342, 0x0000}},
	{0x1FD7, [3]rune{0x0399, 0x0308, 0x0342}},
	{0x1FE2, [3]rune{0x03A5, 0x0308, 0x0300}},
	{0x1FE3, [3]rune{0x03A5, 0x0308, 0x0301}},
	{0x1FE4, [3]rune{0x03A1, 0x0313, 0x0000}},
	{0x1FE6, [3]rune{0x03A5, 0x0342, 0x0000}},
	{0x1FE7, [3]rune{0x03A5, 0x0308, 0x0342}},
	{0x1FF2, [3]rune{0x1FFA, 0x0399, 0x0000}},
	{0x1FF3, [3]rune{0x03A9, 0x0399, 0x0000}},
	{0x1FF4, [3]rune{0x038F, 0x0399, 0x0000}},
	{0x1FF6, [3]rune{0x03A9, 0x0342, 0x0000}},
	{0x1FF7, [3]rune{0x03A9, 0x0342, 0x0399}},
	{0x1FFC, [3]rune{0x03A9, 0x0399, 0x0000}},
	{0xFB00, [3]rune{0x0046, 0x0046, 0x0000}},
	{0xFB01, [3]rune{0x0046, 0x0049, 0x0000}},
	{0xFB02, [3]rune{0x0046, 0x004C, 0x0000}},
	{0xFB03, [3]rune{0x0046, 0x0046, 0x0049}},
	{0xFB04, [3]rune{0x0046, 0x0046, 0x004C}},
	{0xFB05, [3]rune{0x0053, 0x0054, 0x0000}},
	{0xFB06, [3]rune{0x0053, 0x0054, 0x0000}},
	{0xFB13, [3]rune{0x0544, 0x0546, 0x0000}},
	{0xFB14, [3]rune{0x0544, 0x0535, 0x0000}},
	{0xFB15, [3]rune{0x0544, 0x053B, 0x0000}},
	{0xFB16, [3]rune{0x054E, 0x0546, 0x0000}},
	{0xFB17, [3]rune{0x0544, 0x053D, 0x0000}},
}

var fullLower = []fullCase{
	{0x0130, [3]rune{0x0069, 0x0307, 0x0000}},
}

// foldExceptions is the CaseFolding data, minus everything the simple
// lowercase mapping already gets right. Folding is a THIRD operation,
// not a synonym for lowercasing: `ß` folds to `ss` (lowercase leaves it
// alone), `ſ` LATIN SMALL LETTER LONG S folds to `s`, and `µ` MICRO SIGN
// folds to the Greek `μ`. Only the 297 code points where the fold differs
// from `to_lower_char` are stored; everything else falls through to the
// simple lowercase table, which keeps this table a quarter the size of a
// complete one.
//
// Same provenance caveat as fullUpper/fullLower: Go's unicode package has
// `SimpleFold`, which walks orbits rather than yielding a fold string, and
// nothing for full folding — so this is transcribed data, validated
// end-to-end against CPython's str.casefold and pinned by
// TestFoldKnownAnswers.

var foldExceptions = []fullCase{
	{0x00B5, [3]rune{0x03BC, 0x0000, 0x0000}},
	{0x00DF, [3]rune{0x0073, 0x0073, 0x0000}},
	{0x0149, [3]rune{0x02BC, 0x006E, 0x0000}},
	{0x017F, [3]rune{0x0073, 0x0000, 0x0000}},
	{0x01F0, [3]rune{0x006A, 0x030C, 0x0000}},
	{0x0345, [3]rune{0x03B9, 0x0000, 0x0000}},
	{0x0390, [3]rune{0x03B9, 0x0308, 0x0301}},
	{0x03B0, [3]rune{0x03C5, 0x0308, 0x0301}},
	{0x03C2, [3]rune{0x03C3, 0x0000, 0x0000}},
	{0x03D0, [3]rune{0x03B2, 0x0000, 0x0000}},
	{0x03D1, [3]rune{0x03B8, 0x0000, 0x0000}},
	{0x03D5, [3]rune{0x03C6, 0x0000, 0x0000}},
	{0x03D6, [3]rune{0x03C0, 0x0000, 0x0000}},
	{0x03F0, [3]rune{0x03BA, 0x0000, 0x0000}},
	{0x03F1, [3]rune{0x03C1, 0x0000, 0x0000}},
	{0x03F5, [3]rune{0x03B5, 0x0000, 0x0000}},
	{0x0587, [3]rune{0x0565, 0x0582, 0x0000}},
	{0x13A0, [3]rune{0x13A0, 0x0000, 0x0000}},
	{0x13A1, [3]rune{0x13A1, 0x0000, 0x0000}},
	{0x13A2, [3]rune{0x13A2, 0x0000, 0x0000}},
	{0x13A3, [3]rune{0x13A3, 0x0000, 0x0000}},
	{0x13A4, [3]rune{0x13A4, 0x0000, 0x0000}},
	{0x13A5, [3]rune{0x13A5, 0x0000, 0x0000}},
	{0x13A6, [3]rune{0x13A6, 0x0000, 0x0000}},
	{0x13A7, [3]rune{0x13A7, 0x0000, 0x0000}},
	{0x13A8, [3]rune{0x13A8, 0x0000, 0x0000}},
	{0x13A9, [3]rune{0x13A9, 0x0000, 0x0000}},
	{0x13AA, [3]rune{0x13AA, 0x0000, 0x0000}},
	{0x13AB, [3]rune{0x13AB, 0x0000, 0x0000}},
	{0x13AC, [3]rune{0x13AC, 0x0000, 0x0000}},
	{0x13AD, [3]rune{0x13AD, 0x0000, 0x0000}},
	{0x13AE, [3]rune{0x13AE, 0x0000, 0x0000}},
	{0x13AF, [3]rune{0x13AF, 0x0000, 0x0000}},
	{0x13B0, [3]rune{0x13B0, 0x0000, 0x0000}},
	{0x13B1, [3]rune{0x13B1, 0x0000, 0x0000}},
	{0x13B2, [3]rune{0x13B2, 0x0000, 0x0000}},
	{0x13B3, [3]rune{0x13B3, 0x0000, 0x0000}},
	{0x13B4, [3]rune{0x13B4, 0x0000, 0x0000}},
	{0x13B5, [3]rune{0x13B5, 0x0000, 0x0000}},
	{0x13B6, [3]rune{0x13B6, 0x0000, 0x0000}},
	{0x13B7, [3]rune{0x13B7, 0x0000, 0x0000}},
	{0x13B8, [3]rune{0x13B8, 0x0000, 0x0000}},
	{0x13B9, [3]rune{0x13B9, 0x0000, 0x0000}},
	{0x13BA, [3]rune{0x13BA, 0x0000, 0x0000}},
	{0x13BB, [3]rune{0x13BB, 0x0000, 0x0000}},
	{0x13BC, [3]rune{0x13BC, 0x0000, 0x0000}},
	{0x13BD, [3]rune{0x13BD, 0x0000, 0x0000}},
	{0x13BE, [3]rune{0x13BE, 0x0000, 0x0000}},
	{0x13BF, [3]rune{0x13BF, 0x0000, 0x0000}},
	{0x13C0, [3]rune{0x13C0, 0x0000, 0x0000}},
	{0x13C1, [3]rune{0x13C1, 0x0000, 0x0000}},
	{0x13C2, [3]rune{0x13C2, 0x0000, 0x0000}},
	{0x13C3, [3]rune{0x13C3, 0x0000, 0x0000}},
	{0x13C4, [3]rune{0x13C4, 0x0000, 0x0000}},
	{0x13C5, [3]rune{0x13C5, 0x0000, 0x0000}},
	{0x13C6, [3]rune{0x13C6, 0x0000, 0x0000}},
	{0x13C7, [3]rune{0x13C7, 0x0000, 0x0000}},
	{0x13C8, [3]rune{0x13C8, 0x0000, 0x0000}},
	{0x13C9, [3]rune{0x13C9, 0x0000, 0x0000}},
	{0x13CA, [3]rune{0x13CA, 0x0000, 0x0000}},
	{0x13CB, [3]rune{0x13CB, 0x0000, 0x0000}},
	{0x13CC, [3]rune{0x13CC, 0x0000, 0x0000}},
	{0x13CD, [3]rune{0x13CD, 0x0000, 0x0000}},
	{0x13CE, [3]rune{0x13CE, 0x0000, 0x0000}},
	{0x13CF, [3]rune{0x13CF, 0x0000, 0x0000}},
	{0x13D0, [3]rune{0x13D0, 0x0000, 0x0000}},
	{0x13D1, [3]rune{0x13D1, 0x0000, 0x0000}},
	{0x13D2, [3]rune{0x13D2, 0x0000, 0x0000}},
	{0x13D3, [3]rune{0x13D3, 0x0000, 0x0000}},
	{0x13D4, [3]rune{0x13D4, 0x0000, 0x0000}},
	{0x13D5, [3]rune{0x13D5, 0x0000, 0x0000}},
	{0x13D6, [3]rune{0x13D6, 0x0000, 0x0000}},
	{0x13D7, [3]rune{0x13D7, 0x0000, 0x0000}},
	{0x13D8, [3]rune{0x13D8, 0x0000, 0x0000}},
	{0x13D9, [3]rune{0x13D9, 0x0000, 0x0000}},
	{0x13DA, [3]rune{0x13DA, 0x0000, 0x0000}},
	{0x13DB, [3]rune{0x13DB, 0x0000, 0x0000}},
	{0x13DC, [3]rune{0x13DC, 0x0000, 0x0000}},
	{0x13DD, [3]rune{0x13DD, 0x0000, 0x0000}},
	{0x13DE, [3]rune{0x13DE, 0x0000, 0x0000}},
	{0x13DF, [3]rune{0x13DF, 0x0000, 0x0000}},
	{0x13E0, [3]rune{0x13E0, 0x0000, 0x0000}},
	{0x13E1, [3]rune{0x13E1, 0x0000, 0x0000}},
	{0x13E2, [3]rune{0x13E2, 0x0000, 0x0000}},
	{0x13E3, [3]rune{0x13E3, 0x0000, 0x0000}},
	{0x13E4, [3]rune{0x13E4, 0x0000, 0x0000}},
	{0x13E5, [3]rune{0x13E5, 0x0000, 0x0000}},
	{0x13E6, [3]rune{0x13E6, 0x0000, 0x0000}},
	{0x13E7, [3]rune{0x13E7, 0x0000, 0x0000}},
	{0x13E8, [3]rune{0x13E8, 0x0000, 0x0000}},
	{0x13E9, [3]rune{0x13E9, 0x0000, 0x0000}},
	{0x13EA, [3]rune{0x13EA, 0x0000, 0x0000}},
	{0x13EB, [3]rune{0x13EB, 0x0000, 0x0000}},
	{0x13EC, [3]rune{0x13EC, 0x0000, 0x0000}},
	{0x13ED, [3]rune{0x13ED, 0x0000, 0x0000}},
	{0x13EE, [3]rune{0x13EE, 0x0000, 0x0000}},
	{0x13EF, [3]rune{0x13EF, 0x0000, 0x0000}},
	{0x13F0, [3]rune{0x13F0, 0x0000, 0x0000}},
	{0x13F1, [3]rune{0x13F1, 0x0000, 0x0000}},
	{0x13F2, [3]rune{0x13F2, 0x0000, 0x0000}},
	{0x13F3, [3]rune{0x13F3, 0x0000, 0x0000}},
	{0x13F4, [3]rune{0x13F4, 0x0000, 0x0000}},
	{0x13F5, [3]rune{0x13F5, 0x0000, 0x0000}},
	{0x13F8, [3]rune{0x13F0, 0x0000, 0x0000}},
	{0x13F9, [3]rune{0x13F1, 0x0000, 0x0000}},
	{0x13FA, [3]rune{0x13F2, 0x0000, 0x0000}},
	{0x13FB, [3]rune{0x13F3, 0x0000, 0x0000}},
	{0x13FC, [3]rune{0x13F4, 0x0000, 0x0000}},
	{0x13FD, [3]rune{0x13F5, 0x0000, 0x0000}},
	{0x1C80, [3]rune{0x0432, 0x0000, 0x0000}},
	{0x1C81, [3]rune{0x0434, 0x0000, 0x0000}},
	{0x1C82, [3]rune{0x043E, 0x0000, 0x0000}},
	{0x1C83, [3]rune{0x0441, 0x0000, 0x0000}},
	{0x1C84, [3]rune{0x0442, 0x0000, 0x0000}},
	{0x1C85, [3]rune{0x0442, 0x0000, 0x0000}},
	{0x1C86, [3]rune{0x044A, 0x0000, 0x0000}},
	{0x1C87, [3]rune{0x0463, 0x0000, 0x0000}},
	{0x1C88, [3]rune{0xA64B, 0x0000, 0x0000}},
	{0x1E96, [3]rune{0x0068, 0x0331, 0x0000}},
	{0x1E97, [3]rune{0x0074, 0x0308, 0x0000}},
	{0x1E98, [3]rune{0x0077, 0x030A, 0x0000}},
	{0x1E99, [3]rune{0x0079, 0x030A, 0x0000}},
	{0x1E9A, [3]rune{0x0061, 0x02BE, 0x0000}},
	{0x1E9B, [3]rune{0x1E61, 0x0000, 0x0000}},
	{0x1E9E, [3]rune{0x0073, 0x0073, 0x0000}},
	{0x1F50, [3]rune{0x03C5, 0x0313, 0x0000}},
	{0x1F52, [3]rune{0x03C5, 0x0313, 0x0300}},
	{0x1F54, [3]rune{0x03C5, 0x0313, 0x0301}},
	{0x1F56, [3]rune{0x03C5, 0x0313, 0x0342}},
	{0x1F80, [3]rune{0x1F00, 0x03B9, 0x0000}},
	{0x1F81, [3]rune{0x1F01, 0x03B9, 0x0000}},
	{0x1F82, [3]rune{0x1F02, 0x03B9, 0x0000}},
	{0x1F83, [3]rune{0x1F03, 0x03B9, 0x0000}},
	{0x1F84, [3]rune{0x1F04, 0x03B9, 0x0000}},
	{0x1F85, [3]rune{0x1F05, 0x03B9, 0x0000}},
	{0x1F86, [3]rune{0x1F06, 0x03B9, 0x0000}},
	{0x1F87, [3]rune{0x1F07, 0x03B9, 0x0000}},
	{0x1F88, [3]rune{0x1F00, 0x03B9, 0x0000}},
	{0x1F89, [3]rune{0x1F01, 0x03B9, 0x0000}},
	{0x1F8A, [3]rune{0x1F02, 0x03B9, 0x0000}},
	{0x1F8B, [3]rune{0x1F03, 0x03B9, 0x0000}},
	{0x1F8C, [3]rune{0x1F04, 0x03B9, 0x0000}},
	{0x1F8D, [3]rune{0x1F05, 0x03B9, 0x0000}},
	{0x1F8E, [3]rune{0x1F06, 0x03B9, 0x0000}},
	{0x1F8F, [3]rune{0x1F07, 0x03B9, 0x0000}},
	{0x1F90, [3]rune{0x1F20, 0x03B9, 0x0000}},
	{0x1F91, [3]rune{0x1F21, 0x03B9, 0x0000}},
	{0x1F92, [3]rune{0x1F22, 0x03B9, 0x0000}},
	{0x1F93, [3]rune{0x1F23, 0x03B9, 0x0000}},
	{0x1F94, [3]rune{0x1F24, 0x03B9, 0x0000}},
	{0x1F95, [3]rune{0x1F25, 0x03B9, 0x0000}},
	{0x1F96, [3]rune{0x1F26, 0x03B9, 0x0000}},
	{0x1F97, [3]rune{0x1F27, 0x03B9, 0x0000}},
	{0x1F98, [3]rune{0x1F20, 0x03B9, 0x0000}},
	{0x1F99, [3]rune{0x1F21, 0x03B9, 0x0000}},
	{0x1F9A, [3]rune{0x1F22, 0x03B9, 0x0000}},
	{0x1F9B, [3]rune{0x1F23, 0x03B9, 0x0000}},
	{0x1F9C, [3]rune{0x1F24, 0x03B9, 0x0000}},
	{0x1F9D, [3]rune{0x1F25, 0x03B9, 0x0000}},
	{0x1F9E, [3]rune{0x1F26, 0x03B9, 0x0000}},
	{0x1F9F, [3]rune{0x1F27, 0x03B9, 0x0000}},
	{0x1FA0, [3]rune{0x1F60, 0x03B9, 0x0000}},
	{0x1FA1, [3]rune{0x1F61, 0x03B9, 0x0000}},
	{0x1FA2, [3]rune{0x1F62, 0x03B9, 0x0000}},
	{0x1FA3, [3]rune{0x1F63, 0x03B9, 0x0000}},
	{0x1FA4, [3]rune{0x1F64, 0x03B9, 0x0000}},
	{0x1FA5, [3]rune{0x1F65, 0x03B9, 0x0000}},
	{0x1FA6, [3]rune{0x1F66, 0x03B9, 0x0000}},
	{0x1FA7, [3]rune{0x1F67, 0x03B9, 0x0000}},
	{0x1FA8, [3]rune{0x1F60, 0x03B9, 0x0000}},
	{0x1FA9, [3]rune{0x1F61, 0x03B9, 0x0000}},
	{0x1FAA, [3]rune{0x1F62, 0x03B9, 0x0000}},
	{0x1FAB, [3]rune{0x1F63, 0x03B9, 0x0000}},
	{0x1FAC, [3]rune{0x1F64, 0x03B9, 0x0000}},
	{0x1FAD, [3]rune{0x1F65, 0x03B9, 0x0000}},
	{0x1FAE, [3]rune{0x1F66, 0x03B9, 0x0000}},
	{0x1FAF, [3]rune{0x1F67, 0x03B9, 0x0000}},
	{0x1FB2, [3]rune{0x1F70, 0x03B9, 0x0000}},
	{0x1FB3, [3]rune{0x03B1, 0x03B9, 0x0000}},
	{0x1FB4, [3]rune{0x03AC, 0x03B9, 0x0000}},
	{0x1FB6, [3]rune{0x03B1, 0x0342, 0x0000}},
	{0x1FB7, [3]rune{0x03B1, 0x0342, 0x03B9}},
	{0x1FBC, [3]rune{0x03B1, 0x03B9, 0x0000}},
	{0x1FBE, [3]rune{0x03B9, 0x0000, 0x0000}},
	{0x1FC2, [3]rune{0x1F74, 0x03B9, 0x0000}},
	{0x1FC3, [3]rune{0x03B7, 0x03B9, 0x0000}},
	{0x1FC4, [3]rune{0x03AE, 0x03B9, 0x0000}},
	{0x1FC6, [3]rune{0x03B7, 0x0342, 0x0000}},
	{0x1FC7, [3]rune{0x03B7, 0x0342, 0x03B9}},
	{0x1FCC, [3]rune{0x03B7, 0x03B9, 0x0000}},
	{0x1FD2, [3]rune{0x03B9, 0x0308, 0x0300}},
	{0x1FD3, [3]rune{0x03B9, 0x0308, 0x0301}},
	{0x1FD6, [3]rune{0x03B9, 0x0342, 0x0000}},
	{0x1FD7, [3]rune{0x03B9, 0x0308, 0x0342}},
	{0x1FE2, [3]rune{0x03C5, 0x0308, 0x0300}},
	{0x1FE3, [3]rune{0x03C5, 0x0308, 0x0301}},
	{0x1FE4, [3]rune{0x03C1, 0x0313, 0x0000}},
	{0x1FE6, [3]rune{0x03C5, 0x0342, 0x0000}},
	{0x1FE7, [3]rune{0x03C5, 0x0308, 0x0342}},
	{0x1FF2, [3]rune{0x1F7C, 0x03B9, 0x0000}},
	{0x1FF3, [3]rune{0x03C9, 0x03B9, 0x0000}},
	{0x1FF4, [3]rune{0x03CE, 0x03B9, 0x0000}},
	{0x1FF6, [3]rune{0x03C9, 0x0342, 0x0000}},
	{0x1FF7, [3]rune{0x03C9, 0x0342, 0x03B9}},
	{0x1FFC, [3]rune{0x03C9, 0x03B9, 0x0000}},
	{0xAB70, [3]rune{0x13A0, 0x0000, 0x0000}},
	{0xAB71, [3]rune{0x13A1, 0x0000, 0x0000}},
	{0xAB72, [3]rune{0x13A2, 0x0000, 0x0000}},
	{0xAB73, [3]rune{0x13A3, 0x0000, 0x0000}},
	{0xAB74, [3]rune{0x13A4, 0x0000, 0x0000}},
	{0xAB75, [3]rune{0x13A5, 0x0000, 0x0000}},
	{0xAB76, [3]rune{0x13A6, 0x0000, 0x0000}},
	{0xAB77, [3]rune{0x13A7, 0x0000, 0x0000}},
	{0xAB78, [3]rune{0x13A8, 0x0000, 0x0000}},
	{0xAB79, [3]rune{0x13A9, 0x0000, 0x0000}},
	{0xAB7A, [3]rune{0x13AA, 0x0000, 0x0000}},
	{0xAB7B, [3]rune{0x13AB, 0x0000, 0x0000}},
	{0xAB7C, [3]rune{0x13AC, 0x0000, 0x0000}},
	{0xAB7D, [3]rune{0x13AD, 0x0000, 0x0000}},
	{0xAB7E, [3]rune{0x13AE, 0x0000, 0x0000}},
	{0xAB7F, [3]rune{0x13AF, 0x0000, 0x0000}},
	{0xAB80, [3]rune{0x13B0, 0x0000, 0x0000}},
	{0xAB81, [3]rune{0x13B1, 0x0000, 0x0000}},
	{0xAB82, [3]rune{0x13B2, 0x0000, 0x0000}},
	{0xAB83, [3]rune{0x13B3, 0x0000, 0x0000}},
	{0xAB84, [3]rune{0x13B4, 0x0000, 0x0000}},
	{0xAB85, [3]rune{0x13B5, 0x0000, 0x0000}},
	{0xAB86, [3]rune{0x13B6, 0x0000, 0x0000}},
	{0xAB87, [3]rune{0x13B7, 0x0000, 0x0000}},
	{0xAB88, [3]rune{0x13B8, 0x0000, 0x0000}},
	{0xAB89, [3]rune{0x13B9, 0x0000, 0x0000}},
	{0xAB8A, [3]rune{0x13BA, 0x0000, 0x0000}},
	{0xAB8B, [3]rune{0x13BB, 0x0000, 0x0000}},
	{0xAB8C, [3]rune{0x13BC, 0x0000, 0x0000}},
	{0xAB8D, [3]rune{0x13BD, 0x0000, 0x0000}},
	{0xAB8E, [3]rune{0x13BE, 0x0000, 0x0000}},
	{0xAB8F, [3]rune{0x13BF, 0x0000, 0x0000}},
	{0xAB90, [3]rune{0x13C0, 0x0000, 0x0000}},
	{0xAB91, [3]rune{0x13C1, 0x0000, 0x0000}},
	{0xAB92, [3]rune{0x13C2, 0x0000, 0x0000}},
	{0xAB93, [3]rune{0x13C3, 0x0000, 0x0000}},
	{0xAB94, [3]rune{0x13C4, 0x0000, 0x0000}},
	{0xAB95, [3]rune{0x13C5, 0x0000, 0x0000}},
	{0xAB96, [3]rune{0x13C6, 0x0000, 0x0000}},
	{0xAB97, [3]rune{0x13C7, 0x0000, 0x0000}},
	{0xAB98, [3]rune{0x13C8, 0x0000, 0x0000}},
	{0xAB99, [3]rune{0x13C9, 0x0000, 0x0000}},
	{0xAB9A, [3]rune{0x13CA, 0x0000, 0x0000}},
	{0xAB9B, [3]rune{0x13CB, 0x0000, 0x0000}},
	{0xAB9C, [3]rune{0x13CC, 0x0000, 0x0000}},
	{0xAB9D, [3]rune{0x13CD, 0x0000, 0x0000}},
	{0xAB9E, [3]rune{0x13CE, 0x0000, 0x0000}},
	{0xAB9F, [3]rune{0x13CF, 0x0000, 0x0000}},
	{0xABA0, [3]rune{0x13D0, 0x0000, 0x0000}},
	{0xABA1, [3]rune{0x13D1, 0x0000, 0x0000}},
	{0xABA2, [3]rune{0x13D2, 0x0000, 0x0000}},
	{0xABA3, [3]rune{0x13D3, 0x0000, 0x0000}},
	{0xABA4, [3]rune{0x13D4, 0x0000, 0x0000}},
	{0xABA5, [3]rune{0x13D5, 0x0000, 0x0000}},
	{0xABA6, [3]rune{0x13D6, 0x0000, 0x0000}},
	{0xABA7, [3]rune{0x13D7, 0x0000, 0x0000}},
	{0xABA8, [3]rune{0x13D8, 0x0000, 0x0000}},
	{0xABA9, [3]rune{0x13D9, 0x0000, 0x0000}},
	{0xABAA, [3]rune{0x13DA, 0x0000, 0x0000}},
	{0xABAB, [3]rune{0x13DB, 0x0000, 0x0000}},
	{0xABAC, [3]rune{0x13DC, 0x0000, 0x0000}},
	{0xABAD, [3]rune{0x13DD, 0x0000, 0x0000}},
	{0xABAE, [3]rune{0x13DE, 0x0000, 0x0000}},
	{0xABAF, [3]rune{0x13DF, 0x0000, 0x0000}},
	{0xABB0, [3]rune{0x13E0, 0x0000, 0x0000}},
	{0xABB1, [3]rune{0x13E1, 0x0000, 0x0000}},
	{0xABB2, [3]rune{0x13E2, 0x0000, 0x0000}},
	{0xABB3, [3]rune{0x13E3, 0x0000, 0x0000}},
	{0xABB4, [3]rune{0x13E4, 0x0000, 0x0000}},
	{0xABB5, [3]rune{0x13E5, 0x0000, 0x0000}},
	{0xABB6, [3]rune{0x13E6, 0x0000, 0x0000}},
	{0xABB7, [3]rune{0x13E7, 0x0000, 0x0000}},
	{0xABB8, [3]rune{0x13E8, 0x0000, 0x0000}},
	{0xABB9, [3]rune{0x13E9, 0x0000, 0x0000}},
	{0xABBA, [3]rune{0x13EA, 0x0000, 0x0000}},
	{0xABBB, [3]rune{0x13EB, 0x0000, 0x0000}},
	{0xABBC, [3]rune{0x13EC, 0x0000, 0x0000}},
	{0xABBD, [3]rune{0x13ED, 0x0000, 0x0000}},
	{0xABBE, [3]rune{0x13EE, 0x0000, 0x0000}},
	{0xABBF, [3]rune{0x13EF, 0x0000, 0x0000}},
	{0xFB00, [3]rune{0x0066, 0x0066, 0x0000}},
	{0xFB01, [3]rune{0x0066, 0x0069, 0x0000}},
	{0xFB02, [3]rune{0x0066, 0x006C, 0x0000}},
	{0xFB03, [3]rune{0x0066, 0x0066, 0x0069}},
	{0xFB04, [3]rune{0x0066, 0x0066, 0x006C}},
	{0xFB05, [3]rune{0x0073, 0x0074, 0x0000}},
	{0xFB06, [3]rune{0x0073, 0x0074, 0x0000}},
	{0xFB13, [3]rune{0x0574, 0x0576, 0x0000}},
	{0xFB14, [3]rune{0x0574, 0x0565, 0x0000}},
	{0xFB15, [3]rune{0x0574, 0x056B, 0x0000}},
	{0xFB16, [3]rune{0x057E, 0x0576, 0x0000}},
	{0xFB17, [3]rune{0x0574, 0x056D, 0x0000}},
}

// fieldChars is the width of one encoded field, and recChars / rangeChars
// the width of one case-table / class-table record. The generated Fern
// decoder hard-codes the same numbers.
const (
	fieldChars = 4
	recChars   = 5 * fieldChars
	rangeChars = 2 * fieldChars
	fullChars  = 4 * fieldChars
)

// enc6 maps a 6-bit value to its alphabet character: 0..43 to '0'..'[',
// 44..63 to ']'..'p'. The gap skips '\'.
func enc6(d int) byte {
	if d < 44 {
		return byte(48 + d)
	}
	return byte(93 + d - 44)
}

// dec6 is enc6's inverse, mirroring the generated `_dig`.
func dec6(c byte) int {
	if c <= 91 {
		return int(c) - 48
	}
	return int(c) - 49
}

// encField appends v as one 4-character big-endian base-64 field.
func encField(b *strings.Builder, v int) {
	if v < 0 || v >= 1<<24 {
		panic(fmt.Sprintf("unicodegen: field %d out of 24-bit range", v))
	}
	b.WriteByte(enc6((v >> 18) & 63))
	b.WriteByte(enc6((v >> 12) & 63))
	b.WriteByte(enc6((v >> 6) & 63))
	b.WriteByte(enc6(v & 63))
}

// decField reads the field at character offset i, mirroring `_fld`.
func decField(t string, i int) int {
	return dec6(t[i])<<18 | dec6(t[i+1])<<12 | dec6(t[i+2])<<6 | dec6(t[i+3])
}

// run is one case-table entry: an inclusive code-point range whose
// upper/lower mappings share a single rule.
type run struct {
	lo, hi rune
	kind   int
	du, dl int // kind 0 only
}

func deltaUpper(r rune) int { return int(unicode.ToUpper(r)) - int(r) }
func deltaLower(r rune) int { return int(unicode.ToLower(r)) - int(r) }

// startsAlt reports whether an alternating run could start at r: r is an
// uppercase letter whose lowercase is the very next code point (the
// Latin-Extended-A shape, Ā ā Ă ă …).
//
// A block starting on the *lowercase* half would need a second pattern.
// No such block exists in Unicode 15.0, so encoding one is not
// implemented — those code points fall back to constant runs, which are
// always correct, just less compact. If a future Unicode version
// introduces one the tables stay correct automatically; only the size
// regresses.
func startsAlt(r rune) bool {
	return deltaUpper(r) == 0 && deltaLower(r) == 1
}

// altHolds reports whether cp continues the alternating run anchored at
// lo: even offsets are the uppercase half, odd offsets the lowercase.
func altHolds(lo, cp rune) bool {
	if (cp-lo)&1 == 0 {
		return deltaUpper(cp) == 0 && deltaLower(cp) == 1
	}
	return deltaUpper(cp) == -1 && deltaLower(cp) == 0
}

// minAltRun is the shortest alternating run worth encoding as one. Below
// this, constant runs encode the same code points in fewer bytes.
const minAltRun = 4

// caseRuns derives the minimal set of case-mapping runs by walking every
// code point and diffing ToUpper/ToLower — never by copying
// unicode.CaseRanges, so the output is correct regardless of that table's
// stride conventions. Code points with no mapping are omitted entirely;
// the decoder returns them unchanged.
func caseRuns() []run {
	var runs []run
	for r := rune(0); r <= unicode.MaxRune; {
		du, dl := deltaUpper(r), deltaLower(r)
		if du == 0 && dl == 0 {
			r++
			continue
		}
		if startsAlt(r) {
			hi := r
			for hi < unicode.MaxRune && altHolds(r, hi+1) {
				hi++
			}
			if hi-r+1 >= minAltRun {
				runs = append(runs, run{lo: r, hi: hi, kind: 1})
				r = hi + 1
				continue
			}
		}
		hi := r
		for hi < unicode.MaxRune && deltaUpper(hi+1) == du && deltaLower(hi+1) == dl {
			hi++
		}
		runs = append(runs, run{lo: r, hi: hi, du: du, dl: dl})
		r = hi + 1
	}
	return runs
}

// encodeCase renders the case runs as the table string.
func encodeCase(runs []run) string {
	var b strings.Builder
	for _, rn := range runs {
		encField(&b, int(rn.lo))
		encField(&b, int(rn.hi))
		encField(&b, rn.kind)
		encField(&b, rn.du+bias)
		encField(&b, rn.dl+bias)
	}
	return b.String()
}

// applyCase decodes `t` for cp exactly as the generated Fern
// `_case_apply` does — the reference the verifier checks against.
func applyCase(t string, cp int, wantUpper bool) int {
	lo, hi := 0, len(t)/recChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * recChars
		rlo := decField(t, base)
		if cp < rlo {
			hi = mid - 1
			continue
		}
		if cp > decField(t, base+fieldChars) {
			lo = mid + 1
			continue
		}
		if decField(t, base+2*fieldChars) == 0 {
			if wantUpper {
				return cp + decField(t, base+3*fieldChars) - bias
			}
			return cp + decField(t, base+4*fieldChars) - bias
		}
		odd := (cp - rlo) & 1
		if wantUpper {
			return cp - odd
		}
		return cp + 1 - odd
	}
	return cp
}

// coalesce enumerates every code point satisfying pred and merges
// contiguous runs into inclusive [lo, hi] ranges. Enumerating and
// coalescing (rather than copying the stdlib RangeTable) keeps the output
// correct regardless of stride and yields the minimal set of ranges for a
// binary search.
func coalesce(pred func(rune) bool) [][2]rune {
	var out [][2]rune
	inRun := false
	var lo, prev rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if pred(r) {
			if !inRun {
				inRun, lo = true, r
			}
			prev = r
		} else if inRun {
			out = append(out, [2]rune{lo, prev})
			inRun = false
		}
	}
	if inRun {
		out = append(out, [2]rune{lo, prev})
	}
	return out
}

func encodeRanges(rs [][2]rune) string {
	var b strings.Builder
	for _, r := range rs {
		encField(&b, int(r[0]))
		encField(&b, int(r[1]))
	}
	return b.String()
}

// encodeFull encodes a full-case table as 4-field records:
//
//	from | to[0] | to[1] | to[2]
//
// Absent trailing code points are 0, which is unambiguous because U+0000
// is never part of a case expansion.
func encodeFull(fs []fullCase) string {
	var b strings.Builder
	for _, f := range fs {
		encField(&b, int(f.from))
		for _, r := range f.to {
			encField(&b, int(r))
		}
	}
	return b.String()
}

// inRanges mirrors the generated `_in_ranges`.
func inRanges(t string, cp int) bool {
	lo, hi := 0, len(t)/rangeChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * rangeChars
		switch {
		case cp < decField(t, base):
			hi = mid - 1
		case cp > decField(t, base+fieldChars):
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// verify decodes every emitted table for every code point and compares
// against the `unicode` package. Any mismatch is a generator bug and
// fails the run rather than shipping a wrong table.
func verify(caseT string, classes map[string]classTable) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got, want := applyCase(caseT, int(r), true), int(unicode.ToUpper(r)); got != want {
			panic(fmt.Sprintf("unicodegen: to_upper(U+%04X) = U+%04X, want U+%04X", r, got, want))
		}
		if got, want := applyCase(caseT, int(r), false), int(unicode.ToLower(r)); got != want {
			panic(fmt.Sprintf("unicodegen: to_lower(U+%04X) = U+%04X, want U+%04X", r, got, want))
		}
	}
	names := make([]string, 0, len(classes))
	for name := range classes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := classes[name]
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if got, want := inRanges(c.table, int(r)), c.pred(r); got != want {
				panic(fmt.Sprintf("unicodegen: %s(U+%04X) = %v, want %v", name, r, got, want))
			}
		}
	}
}

type classTable struct {
	table string
	pred  func(rune) bool
	count int
}

// emitTable writes a non-public `name(): string` returning the encoded
// table, with a comment recording its entry count and byte size.
func emitTable(b *strings.Builder, name, comment, table string, entries, width int) {
	fmt.Fprintf(b, "// %s\n// %d entries, %d bytes (%d chars per entry).\nfunction %s(): string {\n    return \"%s\";\n}\n\n",
		comment, entries, len(table), width, name, table)
}

// tables builds the case table and every class table, verified against
// the `unicode` package before it is returned.
func tables() (caseT string, runs []run, classes map[string]classTable) {
	runs = caseRuns()
	caseT = encodeCase(runs)

	classes = map[string]classTable{}
	addClass := func(name string, pred func(rune) bool) {
		rs := coalesce(pred)
		classes[name] = classTable{table: encodeRanges(rs), pred: pred, count: len(rs)}
	}
	addClass("letter", unicode.IsLetter)
	addClass("digit", func(r rune) bool { return unicode.Is(unicode.Nd, r) })
	addClass("space", unicode.IsSpace)
	addClass("upper", unicode.IsUpper)
	addClass("lower", unicode.IsLower)
	addClass("cased", isCased)
	addClass("caseignorable", isCaseIgnorable)

	verify(caseT, classes)
	return caseT, runs, classes
}

// wordBreakMid is the Word_Break = MidLetter / MidNumLet / Single_Quote
// set. Go's unicode package exposes the General_Categories and the
// Other_* properties but not the Word_Break property, and this is the one
// piece of Case_Ignorable that comes from it — 17 code points, fixed for
// many Unicode versions.
var wordBreakMid = map[rune]bool{
	0x0027: true, // Single_Quote  APOSTROPHE
	0x002E: true, // MidNumLet     FULL STOP
	0x003A: true, // MidLetter     COLON
	0x00B7: true, // MidLetter     MIDDLE DOT
	0x0387: true, // MidLetter     GREEK ANO TELEIA
	0x055F: true, // MidLetter     ARMENIAN ABBREVIATION MARK
	0x05F4: true, // MidLetter     HEBREW PUNCTUATION GERSHAYIM
	0x2018: true, // MidNumLet     LEFT SINGLE QUOTATION MARK
	0x2019: true, // MidNumLet     RIGHT SINGLE QUOTATION MARK
	0x2024: true, // MidNumLet     ONE DOT LEADER
	0x2027: true, // MidLetter     HYPHENATION POINT
	0xFE13: true, // MidLetter     PRESENTATION FORM FOR VERTICAL COLON
	0xFE52: true, // MidNumLet     SMALL FULL STOP
	0xFE55: true, // MidLetter     SMALL COLON
	0xFF07: true, // MidNumLet     FULLWIDTH APOSTROPHE
	0xFF0E: true, // MidNumLet     FULLWIDTH FULL STOP
	0xFF1A: true, // MidLetter     FULLWIDTH COLON
}

// isCased is the Unicode Cased property: Lu + Ll + Lt + Other_Uppercase +
// Other_Lowercase. Final_Sigma is defined in terms of it.
func isCased(r rune) bool {
	return unicode.Is(unicode.Lu, r) || unicode.Is(unicode.Ll, r) ||
		unicode.Is(unicode.Lt, r) ||
		unicode.Is(unicode.Other_Uppercase, r) ||
		unicode.Is(unicode.Other_Lowercase, r)
}

// isCaseIgnorable is the Unicode Case_Ignorable property: Mn + Me + Cf +
// Lm + Sk, plus the Word_Break mid set above. Final_Sigma skips over these
// when looking for the cased characters on either side, which is what
// makes "ΑΣ'" lowercase its sigma as final rather than medial.
func isCaseIgnorable(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) ||
		unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Lm, r) ||
		unicode.Is(unicode.Sk, r) || wordBreakMid[r]
}

// genStats is what main reports after a run; the tests ignore it.
type genStats struct{ runs, tableBytes int }

// generate renders the complete std/unicode.fern source. Split out of
// main so the tests can regenerate in memory and compare against the
// committed file.
func generate() (string, genStats) {
	caseT, runs, classes := tables()
	stats := genStats{runs: len(runs), tableBytes: len(caseT)}
	for _, c := range classes {
		stats.tableBytes += len(c.table)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `// std/unicode — Unicode case mapping + character classes.
//
// GENERATED by cmd/unicodegen from the Go standard library's unicode
// package (Unicode %s). Do NOT edit by hand — re-run
// `+"`go run ./cmd/unicodegen internal/stdlib/std/unicode.fern`"+` to
// refresh after a Go toolchain upgrade.
//
// What std/string's plain-named case methods delegate to, and the
// complement to its byte-wise to_ascii_* twins. Two families:
//   - Case mapping: to_upper / to_lower / to_upper_char / to_lower_char
//     / swap_case / capitalize / title_case / eq_ignore_case — over
//     every code point (Latin, Greek, Cyrillic, Armenian, fullwidth, …),
//     decoding UTF-8 via std/utf8 and re-encoding. See the caveats below
//     for which mappings are full and which are simple.
//   - Character classes: is_letter / is_digit / is_alnum / is_whitespace
//     / is_upper / is_lower over a code point, via range binary search.
//
// Scope + caveats:
//   - String-level to_upper / to_lower do FULL mapping: a code point may
//     expand to several (`+"`ß`"+` → `+"`SS`"+`, the ligatures, the Greek
//     iota-subscript forms). The per-code-point to_upper_char /
//     to_lower_char stay SIMPLE (1:1) — a 1→N expansion has no single
//     code point to return.
//   - One CONDITIONAL rule is applied when lowercasing: Greek Final_Sigma,
//     so a word-final `+"`Σ`"+` becomes `+"`ς`"+` and a medial one `+"`σ`"+`. The LOCALE
//     tailorings (Turkish dotless i, Lithuanian) are not, by design.
//   - is_digit is the decimal-digit class (Unicode Nd); is_letter is the
//     full Letter category; is_whitespace matches unicode.IsSpace.
//   - Not locale-aware (no Turkish / Lithuanian tailoring), by design.
//
// Representation: every table below is a STRING literal, not an array —
// static rodata, so a lookup allocates nothing and no table is ever
// "built". Fields are 4 characters of a 6-bit alphabet (24 bits each),
// over the two printable spans 48..91 and 93..112 so no character ever
// needs an escape. Deltas are biased by 2^23. See cmd/unicodegen for the
// record layouts and the generator-side round-trip verification.
//
// When const/static arrays land as a language feature (COMPTIME-BRIEF),
// this encoding should be DELETED in favour of them — it exists only
// because a Fern array literal is executable code.
//
// Every entry point ASCII-fast-paths before touching a table: the
// tables cover ASCII too, but the common case shouldn't pay a binary
// search for it.

import "std/utf8" as utf8;

`, unicode.Version)

	emitTable(&b, "_case_table",
		"Case-mapping runs: lo | hi | kind | dUpper+2^23 | dLower+2^23.\n// kind 0 = constant deltas; kind 1 = alternating pairs, even offset\n// from lo is the uppercase half.",
		caseT, len(runs), recChars)
	for _, name := range []string{"letter", "digit", "space", "upper", "lower", "cased", "caseignorable"} {
		c := classes[name]
		emitTable(&b, "_"+name+"_ranges",
			fmt.Sprintf("Inclusive code-point ranges for the %s class: lo | hi.", name),
			c.table, c.count, rangeChars)
	}
	emitTable(&b, "_full_upper_table",
		"Full (1->N) uppercase: from | to0 | to1 | to2, trailing 0 = absent.\n// The unconditional SpecialCasing set — what the 1:1 table cannot say.",
		encodeFull(fullUpper), len(fullUpper), fullChars)
	emitTable(&b, "_full_lower_table",
		"Full (1->N) lowercase: from | to0 | to1 | to2, trailing 0 = absent.",
		encodeFull(fullLower), len(fullLower), fullChars)
	emitTable(&b, "_fold_table",
		"Case FOLDING deltas: from | to0 | to1 | to2, trailing 0 = absent.\n// Only where the fold differs from simple lowercase — everything else\n// falls through to the lowercase table.",
		encodeFull(foldExceptions), len(foldExceptions), fullChars)

	b.WriteString(`// _dig decodes one table character to its 6-bit value. The alphabet
// skips ` + "`\\`" + ` (92), so the two spans are 48..91 and 93..112.
function _dig(c: i32): i32 {
    if (c <= 91) { return c - 48; }
    return c - 49;
}

// _fld reads the 24-bit field at character offset ` + "`i`" + `.
function _fld(t: string, i: i32): i32 {
    return (_dig(t[i]) << 18) | (_dig(t[i + 1]) << 12) | (_dig(t[i + 2]) << 6) | _dig(t[i + 3]);
}

// _case_apply maps ` + "`cp`" + ` through the case table — uppercase when
// ` + "`want_upper`" + `, lowercase otherwise — returning ` + "`cp`" + ` unchanged when it
// has no mapping. Binary search over 20-character records; no allocation.
function _case_apply(cp: i32, want_upper: boolean): i32 {
    var t: string = _case_table();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 20 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 20;
        var rlo: i32 = _fld(t, base);
        if (cp < rlo) {
            hi = mid - 1;
        } else if (cp > _fld(t, base + 4)) {
            lo = mid + 1;
        } else {
            if (_fld(t, base + 8) == 0) {
                if (want_upper) { return cp + _fld(t, base + 12) - 8388608; }
                return cp + _fld(t, base + 16) - 8388608;
            }
            var odd: i32 = (cp - rlo) & 1;
            if (want_upper) { return cp - odd; }
            return cp + 1 - odd;
        }
    }
    return cp;
}

// ` + "`to_upper_char(cp)`" + ` — the uppercase of a single code point (its
// simple mapping), or ` + "`cp`" + ` unchanged when it has none.
pub function to_upper_char(cp: i32): i32 {
    if (cp < 128) {
        if (cp >= 97 && cp <= 122) { return cp - 32; }
        return cp;
    }
    return _case_apply(cp, true);
}

// ` + "`to_lower_char(cp)`" + ` — the lowercase of a single code point.
pub function to_lower_char(cp: i32): i32 {
    if (cp < 128) {
        if (cp >= 65 && cp <= 90) { return cp + 32; }
        return cp;
    }
    return _case_apply(cp, false);
}

// _is_ascii reports whether every byte of ` + "`s`" + ` is below 0x80, so the
// whole string can take the byte-fold fast path.
function _is_ascii(s: string): boolean {
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        if (s[i] >= 128) { return false; }
        i = i + 1;
    }
    return true;
}

// _ascii_fold remaps the 26 letters starting at ` + "`from`" + ` to those starting
// at ` + "`to`" + `, leaving every other byte alone. The fast path for
// to_upper / to_lower on ASCII input.
function _ascii_fold(s: string, from: i32, to: i32): string {
    var n: i32 = s.len();
    var buf: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i];
        if (b >= from && b < from + 26) { b = b + (to - from); }
        buf = buf.with(i, b as u8);
        i = i + 1;
    }
    return string_from_bytes(buf);
}

// _full_case returns the FULL (1->N) mapping of ` + "`cp`" + ` as an already
// encoded string, or "" when ` + "`cp`" + ` has none and the simple table applies.
// Binary search over 16-character records; a trailing 0 field means the
// expansion is shorter than 3 code points.
function _full_case(cp: i32, want_upper: boolean): string {
    var t: string = _full_lower_table();
    if (want_upper) { t = _full_upper_table(); }
    var lo: i32 = 0;
    var hi: i32 = t.len() / 16 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 16;
        var from: i32 = _fld(t, base);
        if (cp < from) {
            hi = mid - 1;
        } else if (cp > from) {
            lo = mid + 1;
        } else {
            var out: string = utf8.utf8_encode(_fld(t, base + 4));
            var c2: i32 = _fld(t, base + 8);
            if (c2 != 0) { out = out + utf8.utf8_encode(c2); }
            var c3: i32 = _fld(t, base + 12);
            if (c3 != 0) { out = out + utf8.utf8_encode(c3); }
            return out;
        }
    }
    return "";
}

// _final_sigma reports whether the sigma at byte offset ` + "`at`" + ` in ` + "`s`" + ` is
// word-FINAL in the Unicode sense: preceded by a Cased code point and NOT
// followed by one, skipping Case_Ignorable code points on both sides.
// That is the Final_Sigma condition from SpecialCasing, and it is why
// ` + "`ΣΟΦΟΣ`" + ` lowercases to ` + "`σοφος`" + ` and not ` + "`σοφοσ`" + `.
function _final_sigma(s: string, at: i32, sigma_len: i32): boolean {
    // Look BACK for a cased code point, skipping case-ignorables.
    var before: boolean = false;
    var k: i32 = at;
    while (k > 0) {
        var st: i32 = k - 1;
        while (st > 0 && (s[st] & 192) == 128) { st = st - 1; }
        var cp: i32 = 65533;
        match (utf8.utf8_decode_at(s, st)) {
            Some(pr) => { cp = pr.0; },
            None => { }
        }
        if (_in_ranges(_caseignorable_ranges(), cp)) { k = st; }
        else {
            before = _in_ranges(_cased_ranges(), cp);
            k = 0;
        }
    }
    if (!before) { return false; }
    // Look FORWARD for a cased code point, skipping case-ignorables.
    var n: i32 = s.len();
    var i: i32 = at + sigma_len;
    while (i < n) {
        var cp2: i32 = 65533;
        var w: i32 = 1;
        match (utf8.utf8_decode_at(s, i)) {
            Some(pr2) => { cp2 = pr2.0; w = pr2.1; },
            None => { }
        }
        if (_in_ranges(_caseignorable_ranges(), cp2)) { i = i + w; }
        else { return !_in_ranges(_cased_ranges(), cp2); }
    }
    return true;
}

// _map_upper and _map_lower decode ` + "`s`" + `, map every code point, and
// re-encode. Malformed bytes each become U+FFFD, matching utf8.codepoints.
//
// FULL mapping: a code point with a 1->N expansion (` + "`ß`" + ` -> ` + "`SS`" + `, the
// ligatures, the Greek iota-subscript forms) contributes several code
// points, so the result can be longer than the input in BOTH bytes and
// code points. Everything else takes the simple 1:1 table.
//
// These are deliberately TWO functions rather than one taking a direction
// flag. Only the lowercase path needs Final_Sigma, and Final_Sigma reaches
// the Cased and Case_Ignorable range tables — about 8.9 KB. With a shared
// body, per-function DCE cannot tell that an uppercase-only program never
// reaches them, so every such program paid for tables it could not use.
// Do not merge these back together.
function _map_upper(s: string): string {
    var out: string = "";
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                var full: string = _full_case(pair.0, true);
                if (full.len() > 0) {
                    out = out + full;
                } else {
                    out = out + utf8.utf8_encode(_case_apply(pair.0, true));
                }
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode(65533); i = i + 1; }
        }
    }
    return out;
}

// The lowercase half, plus the one CONDITIONAL rule Fern applies: capital
// sigma (U+03A3) becomes final sigma ` + "`ς`" + ` in word-final position and ` + "`σ`" + `
// elsewhere. The locale tailorings (Turkish, Lithuanian) are not applied.
function _map_lower(s: string): string {
    var out: string = "";
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                if (pair.0 == 931 && _final_sigma(s, i, pair.1)) {
                    out = out + utf8.utf8_encode(962);
                } else {
                    var full: string = _full_case(pair.0, false);
                    if (full.len() > 0) {
                        out = out + full;
                    } else {
                        out = out + utf8.utf8_encode(_case_apply(pair.0, false));
                    }
                }
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode(65533); i = i + 1; }
        }
    }
    return out;
}

// ` + "`to_upper(s)`" + ` — ` + "`s`" + ` uppercased with FULL Unicode mapping, so
// a code point may expand to several (` + "`ß`" + ` -> ` + "`SS`" + `). Pure-ASCII input takes
// a byte fold and never touches a table.
pub function to_upper(s: string): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 97, 65); }
    return _map_upper(s);
}

// ` + "`to_lower(s)`" + ` — ` + "`s`" + ` lowercased with FULL Unicode mapping.
// Greek Final_Sigma IS applied: a word-final ` + "`Σ`" + ` becomes ` + "`ς`" + `, a
// medial one ` + "`σ`" + `.
pub function to_lower(s: string): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 65, 97); }
    return _map_lower(s);
}

// ` + "`case_fold(cp)`" + ` — the case-folded form of one code point, as an
// encoded string. Folding is its own operation, NOT lowercasing: ` + "`ß`" + `
// folds to ` + "`ss`" + ` and ` + "`ſ`" + ` folds to ` + "`s`" + `, neither of which ` + "`to_lower_char`" + `
// does. Only the differing code points are tabulated; the rest fall
// through to the simple lowercase mapping.
function _fold_cp(cp: i32): string {
    var t: string = _fold_table();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 16 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 16;
        var from: i32 = _fld(t, base);
        if (cp < from) {
            hi = mid - 1;
        } else if (cp > from) {
            lo = mid + 1;
        } else {
            var out: string = utf8.utf8_encode(_fld(t, base + 4));
            var c2: i32 = _fld(t, base + 8);
            if (c2 != 0) { out = out + utf8.utf8_encode(c2); }
            var c3: i32 = _fld(t, base + 12);
            if (c3 != 0) { out = out + utf8.utf8_encode(c3); }
            return out;
        }
    }
    return utf8.utf8_encode(to_lower_char(cp));
}

// ` + "`case_fold(s)`" + ` — ` + "`s`" + ` mapped to a form suitable for caseless comparison.
// NOT the same as lowercasing: folding ` + "`ß`" + ` gives ` + "`ss`" + `, so folding two
// strings and comparing catches equivalences ` + "`to_lower`" + ` misses. The
// result is for COMPARISON only — it is not meant to be displayed.
pub function case_fold(s: string): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 65, 97); }
    var out: string = "";
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                out = out + _fold_cp(pair.0);
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode(65533); i = i + 1; }
        }
    }
    return out;
}

// ` + "`eq_ignore_case(a, b)`" + ` — caseless equality under full case FOLDING, so
// ` + "`ß`" + ` and ` + "`ss`" + ` compare equal and ` + "`ſ`" + ` matches ` + "`s`" + `. Pure-ASCII
// operands stream byte-wise and allocate nothing; anything else folds
// both sides, because a fold can change length and so the two cannot be
// walked in lockstep.
pub function eq_ignore_case(a: string, b: string): boolean {
    if (_is_ascii(a) && _is_ascii(b)) {
        var na: i32 = a.len();
        if (na != b.len()) { return false; }
        var k: i32 = 0;
        while (k < na) {
            var ca: i32 = a[k];
            var cb: i32 = b[k];
            if (ca >= 65 && ca <= 90) { ca = ca + 32; }
            if (cb >= 65 && cb <= 90) { cb = cb + 32; }
            if (ca != cb) { return false; }
            k = k + 1;
        }
        return true;
    }
    return case_fold(a) == case_fold(b);
}

// _swap_case_cp toggles one code point's case. Caseless code points
// (digits, punctuation, CJK, …) pass through — ` + "`is_upper`" + ` / ` + "`is_lower`" + `
// are both false for them, so neither branch fires.
function _swap_case_cp(cp: i32): i32 {
    if (is_upper(cp)) { return to_lower_char(cp); }
    if (is_lower(cp)) { return to_upper_char(cp); }
    return cp;
}

// ` + "`swap_case(s)`" + ` — every uppercase code point becomes lowercase and vice
// versa. Its own inverse on any string whose code points round-trip
// under simple mapping.
pub function swap_case(s: string): string {
    if (_is_ascii(s)) {
        var n: i32 = s.len();
        var buf: u8[] = __alloc_u8(n);
        var k: i32 = 0;
        while (k < n) {
            var b: i32 = s[k];
            if (b >= 65 && b <= 90) { b = b + 32; }
            else if (b >= 97 && b <= 122) { b = b - 32; }
            buf = buf.with(k, b as u8);
            k = k + 1;
        }
        return string_from_bytes(buf);
    }
    var out: string = "";
    var len: i32 = s.len();
    var i: i32 = 0;
    while (i < len) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                out = out + utf8.utf8_encode(_swap_case_cp(pair.0));
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode(65533); i = i + 1; }
        }
    }
    return out;
}

// ` + "`capitalize(s)`" + ` — uppercase the FIRST code point, leave the rest
// exactly as they are. Not Python's str.capitalize, which also
// lowercases the tail; preserving the tail is the less lossy default.
pub function capitalize(s: string): string {
    var n: i32 = s.len();
    if (n == 0) { return ""; }
    match (utf8.utf8_decode_at(s, 0)) {
        Some(pair) => {
            var up: i32 = to_upper_char(pair.0);
            if (up == pair.0) { return s; }
            return utf8.utf8_encode(up) + s[pair.1:n];
        },
        None => { return s; }
    }
}

// ` + "`title_case(s)`" + ` — uppercase the first code point of every
// whitespace-separated word, leaving the rest of each word alone (so
// "FOX" stays "FOX"). Word breaks are any Unicode whitespace, not just
// U+0020 — the ASCII ` + "`to_ascii_title_case`" + ` breaks on the space byte
// only, so a tab-separated string titles differently between the two.
// This is a word-BREAK notion, not UAX #29 segmentation (#5633).
pub function title_case(s: string): string {
    var out: string = "";
    var n: i32 = s.len();
    var i: i32 = 0;
    var at_start: boolean = true;
    while (i < n) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                var cp: i32 = pair.0;
                if (at_start) {
                    out = out + utf8.utf8_encode(to_upper_char(cp));
                } else {
                    out = out + utf8.utf8_encode(cp);
                }
                at_start = is_whitespace(cp);
                i = i + pair.1;
            },
            None => {
                out = out + utf8.utf8_encode(65533);
                at_start = false;
                i = i + 1;
            }
        }
    }
    return out;
}

// _in_ranges binary-searches an inclusive-range table (8-character
// lo | hi records, sorted and non-overlapping) for ` + "`cp`" + `.
function _in_ranges(t: string, cp: i32): boolean {
    var lo: i32 = 0;
    var hi: i32 = t.len() / 8 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 8;
        if (cp < _fld(t, base)) { hi = mid - 1; }
        else if (cp > _fld(t, base + 4)) { lo = mid + 1; }
        else { return true; }
    }
    return false;
}

// ` + "`is_letter(cp)`" + ` — is ` + "`cp`" + ` in the Unicode Letter category (L*)?
pub function is_letter(cp: i32): boolean {
    if (cp < 128) { return (cp >= 65 && cp <= 90) || (cp >= 97 && cp <= 122); }
    return _in_ranges(_letter_ranges(), cp);
}

// ` + "`is_digit(cp)`" + ` — is ` + "`cp`" + ` a Unicode decimal digit (category Nd)?
pub function is_digit(cp: i32): boolean {
    if (cp < 128) { return cp >= 48 && cp <= 57; }
    return _in_ranges(_digit_ranges(), cp);
}

// ` + "`is_alnum(cp)`" + ` — a letter or a decimal digit.
pub function is_alnum(cp: i32): boolean {
    return is_letter(cp) || is_digit(cp);
}

// ` + "`is_whitespace(cp)`" + ` — matches Go's unicode.IsSpace (space, tab,
// newline, NBSP, the Unicode space separators, …).
pub function is_whitespace(cp: i32): boolean {
    if (cp < 128) { return cp == 32 || (cp >= 9 && cp <= 13); }
    return _in_ranges(_space_ranges(), cp);
}

// ` + "`is_upper(cp)`" + ` / ` + "`is_lower(cp)`" + ` — Unicode upper/lowercase letters.
pub function is_upper(cp: i32): boolean {
    if (cp < 128) { return cp >= 65 && cp <= 90; }
    return _in_ranges(_upper_ranges(), cp);
}

pub function is_lower(cp: i32): boolean {
    if (cp < 128) { return cp >= 97 && cp <= 122; }
    return _in_ranges(_lower_ranges(), cp);
}

// _all_cp reports whether every code point of ` + "`s`" + ` satisfies the class
// named by ` + "`kind`" + ` (0 letter, 1 letter-or-digit, 2 decimal digit). Empty
// input is FALSE, matching the std/string predicates these back: "every
// character is a letter" is not a useful claim about no characters.
// Malformed bytes decode to U+FFFD, which is in none of the classes, so
// invalid UTF-8 answers false rather than being skipped.
function _all_cp(s: string, kind: i32): boolean {
    var n: i32 = s.len();
    if (n == 0) { return false; }
    var i: i32 = 0;
    while (i < n) {
        var cp: i32 = 65533;
        var w: i32 = 1;
        match (utf8.utf8_decode_at(s, i)) {
            Some(pr) => { cp = pr.0; w = pr.1; },
            None => { }
        }
        if (kind == 0) { if (!is_letter(cp)) { return false; } }
        else if (kind == 1) { if (!is_alnum(cp)) { return false; } }
        else { if (!is_digit(cp)) { return false; } }
        i = i + w;
    }
    return true;
}

// ` + "`is_alpha_only(s)`" + ` — every code point is a Unicode letter, so ` + "`élan`" + ` and
// ` + "`Ελλάδα`" + ` qualify where the byte-wise ASCII check rejects them.
pub function is_alpha_only(s: string): boolean {
    return _all_cp(s, 0);
}

// ` + "`is_alnum_only(s)`" + ` — every code point is a letter or a decimal digit.
pub function is_alnum_only(s: string): boolean {
    return _all_cp(s, 1);
}

// ` + "`is_numeric(s)`" + ` — every code point is a Unicode decimal digit (category
// Nd), which includes the Arabic-Indic and fullwidth digits, not just
// ASCII 0-9. Note this is a DIGIT test, not a number-parse test: it says
// nothing about signs, separators or overflow.
pub function is_numeric(s: string): boolean {
    return _all_cp(s, 2);
}

`)

	return b.String(), stats
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: unicodegen OUTPUT.fern")
		os.Exit(2)
	}
	src, stats := generate()
	if err := os.WriteFile(os.Args[1], []byte(src), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d case runs, %d table bytes, Unicode %s)\n",
		os.Args[1], stats.runs, stats.tableBytes, unicode.Version)
}
