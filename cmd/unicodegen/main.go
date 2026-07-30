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
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strconv"
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

// normdata.txt holds the canonical-normalization source data. It is
// checked in rather than derived here because Go's unicode package ships
// neither canonical decompositions nor combining classes, and this module
// has no external dependencies. gen_normdata.py regenerates it from
// CPython's unicodedata — the same oracle the full case tables came from.
//
//go:embed normdata.txt
var normData string

// cccRun is an inclusive range of code points sharing one combining class.
type cccRun struct {
	lo, hi rune
	ccc    int
}

// decomp is one code point's FULL canonical decomposition, already in
// canonical order. Four elements is the observed maximum (U+1F82 and its
// Greek neighbours); unused trailing slots are 0, which is unambiguous
// because U+0000 never appears in a decomposition.
type decomp struct {
	cp rune
	to [4]rune
}

// primary is a primary composite: to0 + to1 recompose to cp under NFC.
// Composition-excluded characters are simply absent, so the table encodes
// the Full_Composition_Exclusion property by omission.
type primary struct {
	a, b, cp rune
}

const (
	cccChars     = 3 * fieldChars
	decompChars  = 5 * fieldChars
	composeChars = 3 * fieldChars
)

// Hangul syllables decompose arithmetically rather than by table (UAX #15).
// normdata.txt deliberately omits them; the generated Fern implements the
// formula with these constants.
const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulTCount = 28
	hangulNCount = 588
	hangulSCount = 11172
)

// parseNormData reads the embedded data into the three tables. Malformed
// input panics: it is generated and checked in, so a parse failure is a
// broken commit, not a runtime condition to tolerate.
func parseNormData() (cccs []cccRun, decomps []decomp, prims []primary) {
	ccc := map[rune]int{}
	for _, line := range strings.Split(normData, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ";")
		if len(f) < 2 {
			panic("unicodegen: malformed normdata line: " + line)
		}
		cp := rune(mustHex(f[1]))
		switch f[0] {
		case "C":
			v, err := strconv.Atoi(f[2])
			if err != nil {
				panic("unicodegen: bad combining class: " + line)
			}
			ccc[cp] = v
		case "D":
			var d decomp
			d.cp = cp
			parts := strings.Fields(f[2])
			if len(parts) == 0 || len(parts) > len(d.to) {
				panic("unicodegen: decomposition arity out of range: " + line)
			}
			for i, p := range parts {
				d.to[i] = rune(mustHex(p))
			}
			decomps = append(decomps, d)
		case "P":
			parts := strings.Fields(f[2])
			if len(parts) != 2 {
				panic("unicodegen: primary composite must have two parts: " + line)
			}
			prims = append(prims, primary{a: rune(mustHex(parts[0])), b: rune(mustHex(parts[1])), cp: cp})
		default:
			panic("unicodegen: unknown normdata record: " + line)
		}
	}

	// Coalesce equal classes into ranges the same way the character
	// classes do — 912 code points collapse to a few hundred records.
	keys := make([]rune, 0, len(ccc))
	for cp := range ccc {
		keys = append(keys, cp)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, cp := range keys {
		if n := len(cccs); n > 0 && cccs[n-1].hi == cp-1 && cccs[n-1].ccc == ccc[cp] {
			cccs[n-1].hi = cp
			continue
		}
		cccs = append(cccs, cccRun{lo: cp, hi: cp, ccc: ccc[cp]})
	}

	// Both lookup tables are binary-searched by the generated Fern, so
	// they must be sorted by their search key. The composition table is
	// keyed on the PAIR, not on the composite.
	sort.Slice(decomps, func(i, j int) bool { return decomps[i].cp < decomps[j].cp })
	sort.Slice(prims, func(i, j int) bool {
		if prims[i].a != prims[j].a {
			return prims[i].a < prims[j].a
		}
		return prims[i].b < prims[j].b
	})
	return cccs, decomps, prims
}

func mustHex(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 16, 32)
	if err != nil {
		panic("unicodegen: bad hex field " + s)
	}
	return v
}

func encodeCCC(rs []cccRun) string {
	var b strings.Builder
	for _, r := range rs {
		encField(&b, int(r.lo))
		encField(&b, int(r.hi))
		encField(&b, r.ccc)
	}
	return b.String()
}

func encodeDecomp(ds []decomp) string {
	var b strings.Builder
	for _, d := range ds {
		encField(&b, int(d.cp))
		for _, r := range d.to {
			encField(&b, int(r))
		}
	}
	return b.String()
}

func encodeCompose(ps []primary) string {
	var b strings.Builder
	for _, p := range ps {
		encField(&b, int(p.a))
		encField(&b, int(p.b))
		encField(&b, int(p.cp))
	}
	return b.String()
}

// verifyNorm decodes the three emitted tables and checks them against the
// parsed data, the same round-trip discipline verify applies to the case
// tables. It also asserts the invariant the composer relies on: a primary
// composite's pair must itself be canonically ordered, so composing left
// to right can never need to look backwards.
func verifyNorm(cccT, decompT, composeT string, cccs []cccRun, decomps []decomp, prims []primary) {
	classOf := func(cp rune) int {
		for _, r := range cccs {
			if cp >= r.lo && cp <= r.hi {
				return r.ccc
			}
		}
		return 0
	}
	for _, r := range cccs {
		for cp := r.lo; cp <= r.hi; cp++ {
			if got := decodeCCC(cccT, int(cp)); got != r.ccc {
				panic(fmt.Sprintf("unicodegen: ccc(U+%04X) = %d, want %d", cp, got, r.ccc))
			}
		}
	}
	for _, d := range decomps {
		got, ok := decodeDecomp(decompT, int(d.cp))
		if !ok || got != d.to {
			panic(fmt.Sprintf("unicodegen: decomp(U+%04X) = %v (%v), want %v", d.cp, got, ok, d.to))
		}
		if hangulSBase <= d.cp && d.cp < hangulSBase+hangulSCount {
			panic(fmt.Sprintf("unicodegen: Hangul U+%04X must decompose arithmetically, not by table", d.cp))
		}
	}
	for _, p := range prims {
		if got := decodeCompose(composeT, int(p.a), int(p.b)); got != int(p.cp) {
			panic(fmt.Sprintf("unicodegen: compose(U+%04X,U+%04X) = U+%04X, want U+%04X", p.a, p.b, got, p.cp))
		}
		if ca, cb := classOf(p.a), classOf(p.b); ca != 0 && ca > cb {
			panic(fmt.Sprintf("unicodegen: primary composite U+%04X has misordered pair", p.cp))
		}
	}
}

// decodeCCC mirrors the generated _ccc_of.
func decodeCCC(t string, cp int) int {
	lo, hi := 0, len(t)/cccChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * cccChars
		switch {
		case cp < decField(t, base):
			hi = mid - 1
		case cp > decField(t, base+fieldChars):
			lo = mid + 1
		default:
			return decField(t, base+2*fieldChars)
		}
	}
	return 0
}

// decodeDecomp mirrors the generated _decomp_of.
func decodeDecomp(t string, cp int) ([4]rune, bool) {
	lo, hi := 0, len(t)/decompChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * decompChars
		switch {
		case cp < decField(t, base):
			hi = mid - 1
		case cp > decField(t, base):
			lo = mid + 1
		default:
			var out [4]rune
			for i := range out {
				out[i] = rune(decField(t, base+(i+1)*fieldChars))
			}
			return out, true
		}
	}
	return [4]rune{}, false
}

// decodeCompose mirrors the generated _compose_pair, returning 0 for a
// pair with no primary composite.
func decodeCompose(t string, a, b int) int {
	lo, hi := 0, len(t)/composeChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * composeChars
		ka, kb := decField(t, base), decField(t, base+fieldChars)
		switch {
		case a < ka || (a == ka && b < kb):
			hi = mid - 1
		case a > ka || (a == ka && b > kb):
			lo = mid + 1
		default:
			return decField(t, base+2*fieldChars)
		}
	}
	return 0
}

// gcbdata.txt holds the UAX #29 segmentation source data. Like
// normdata.txt it is checked in: neither Go's unicode package nor
// CPython's unicodedata exposes Grapheme_Cluster_Break or
// Extended_Pictographic. gen_gcbdata.py regenerates it from `uniseg`,
// a regeneration-time dependency only.
//
//go:embed gcbdata.txt
var gcbData string

// Grapheme_Cluster_Break class IDs. The generated Fern hard-codes these
// same numbers, so they are part of the table format: renumbering here
// without renumbering there silently changes every boundary decision.
//
// Other is 0 because it is the default the lookup returns for the code
// points the table omits.
const (
	gcbOther = iota
	gcbCR
	gcbLF
	gcbControl
	gcbExtend
	gcbZWJ
	gcbRegionalIndicator
	gcbPrepend
	gcbSpacingMark
	gcbL
	gcbV
	gcbT
	gcbLV
	gcbLVT
	gcbClassCount
)

// gcbNames maps uniseg's enum names onto the IDs above. SpacingMark is
// spelled PACINGMARK upstream; the mapping is the one place that quirk
// is allowed to appear.
var gcbNames = map[string]int{
	"CR": gcbCR, "LF": gcbLF, "CONTROL": gcbControl,
	"EXTEND": gcbExtend, "ZWJ": gcbZWJ,
	"REGIONAL_INDICATOR": gcbRegionalIndicator,
	"PREPEND":            gcbPrepend, "PACINGMARK": gcbSpacingMark,
	"L": gcbL, "V": gcbV, "T": gcbT, "LV": gcbLV, "LVT": gcbLVT,
}

const gcbChars = 3 * fieldChars

type gcbRun struct {
	lo, hi rune
	class  int
}

// parseGCBData reads the embedded segmentation data. Malformed input
// panics: it is generated and checked in, so a parse failure is a broken
// commit rather than a condition to tolerate.
func parseGCBData() (gcbs []gcbRun, extPict [][2]rune, version string) {
	for _, line := range strings.Split(gcbData, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if i := strings.Index(line, "# UCD "); i == 0 {
				version = strings.TrimSpace(strings.TrimPrefix(line, "# UCD "))
			}
			continue
		}
		f := strings.Split(line, ";")
		switch f[0] {
		case "G":
			if len(f) != 4 {
				panic("unicodegen: malformed GCB line: " + line)
			}
			class, ok := gcbNames[f[3]]
			if !ok {
				panic("unicodegen: unknown Grapheme_Cluster_Break class: " + f[3])
			}
			gcbs = append(gcbs, gcbRun{lo: rune(mustHex(f[1])), hi: rune(mustHex(f[2])), class: class})
		case "E":
			if len(f) != 3 {
				panic("unicodegen: malformed Extended_Pictographic line: " + line)
			}
			extPict = append(extPict, [2]rune{rune(mustHex(f[1])), rune(mustHex(f[2]))})
		default:
			panic("unicodegen: unknown gcbdata record: " + line)
		}
	}
	sort.Slice(gcbs, func(i, j int) bool { return gcbs[i].lo < gcbs[j].lo })
	sort.Slice(extPict, func(i, j int) bool { return extPict[i][0] < extPict[j][0] })
	return gcbs, extPict, version
}

func encodeGCB(rs []gcbRun) string {
	var b strings.Builder
	for _, r := range rs {
		encField(&b, int(r.lo))
		encField(&b, int(r.hi))
		encField(&b, r.class)
	}
	return b.String()
}

// decodeGCB mirrors the generated _gcb_of.
func decodeGCB(t string, cp int) int {
	lo, hi := 0, len(t)/gcbChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * gcbChars
		switch {
		case cp < decField(t, base):
			hi = mid - 1
		case cp > decField(t, base+fieldChars):
			lo = mid + 1
		default:
			return decField(t, base+2*fieldChars)
		}
	}
	return gcbOther
}

// verifyGCB round-trips the emitted tables and checks the structural
// invariants the binary search depends on.
func verifyGCB(gcbTable, extT string, gcbs []gcbRun, extPict [][2]rune) {
	for i, r := range gcbs {
		if r.lo > r.hi {
			panic(fmt.Sprintf("unicodegen: GCB range U+%04X..U+%04X inverted", r.lo, r.hi))
		}
		if i > 0 && gcbs[i-1].hi >= r.lo {
			panic(fmt.Sprintf("unicodegen: GCB ranges overlap at U+%04X", r.lo))
		}
		if r.class <= gcbOther || r.class >= gcbClassCount {
			panic(fmt.Sprintf("unicodegen: GCB class %d out of range at U+%04X", r.class, r.lo))
		}
		for _, cp := range []rune{r.lo, r.hi} {
			if got := decodeGCB(gcbTable, int(cp)); got != r.class {
				panic(fmt.Sprintf("unicodegen: gcb(U+%04X) = %d, want %d", cp, got, r.class))
			}
		}
	}
	for i, r := range extPict {
		if r[0] > r[1] {
			panic(fmt.Sprintf("unicodegen: ExtPict range U+%04X..U+%04X inverted", r[0], r[1]))
		}
		if i > 0 && extPict[i-1][1] >= r[0] {
			panic(fmt.Sprintf("unicodegen: ExtPict ranges overlap at U+%04X", r[0]))
		}
		if !inRanges(extT, int(r[0])) || !inRanges(extT, int(r[1])) {
			panic(fmt.Sprintf("unicodegen: ExtPict range U+%04X..U+%04X does not decode", r[0], r[1]))
		}
	}
	// Spot-check the anchors the state machine keys on; a renumbering of
	// the class IDs would sail past the round-trip above but not this.
	for _, tc := range []struct {
		cp    rune
		class int
	}{
		{0x000D, gcbCR}, {0x000A, gcbLF}, {0x200D, gcbZWJ},
		{0x1F1E6, gcbRegionalIndicator}, {0x0300, gcbExtend},
		{0x1100, gcbL}, {0x1161, gcbV}, {0x11A8, gcbT},
		{0xAC00, gcbLV}, {0xAC01, gcbLVT}, {'a', gcbOther},
	} {
		if got := decodeGCB(gcbTable, int(tc.cp)); got != tc.class {
			panic(fmt.Sprintf("unicodegen: gcb(U+%04X) = %d, want %d", tc.cp, got, tc.class))
		}
	}
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

	cccs, decomps, prims := parseNormData()
	cccT, decompT, composeT := encodeCCC(cccs), encodeDecomp(decomps), encodeCompose(prims)
	verifyNorm(cccT, decompT, composeT, cccs, decomps, prims)

	// Both quick-check sets fall out of the tables already parsed, so
	// normdata.txt carries no separate quick-check data. NFD=No is
	// "decomposes at all"; NFC=No is "decomposes but never recomposes",
	// which is the Full_Composition_Exclusion property restated.
	decomposes := make(map[rune]bool, len(decomps))
	for _, d := range decomps {
		decomposes[d.cp] = true
	}
	recomposes := make(map[rune]bool, len(prims))
	for _, p := range prims {
		recomposes[p.cp] = true
	}
	isHangul := func(r rune) bool { return r >= hangulSBase && r < hangulSBase+hangulSCount }
	nfdNo := coalesce(func(r rune) bool { return decomposes[r] || isHangul(r) })
	nfcNo := coalesce(func(r rune) bool { return decomposes[r] && !recomposes[r] })

	// NFC_QC=Maybe: the trailing halves of the primary composites. Their
	// presence cannot be decided by a local test, so the quick check has
	// to escalate to a full comparison — see the generated is_nfc.
	combinesLeft := make(map[rune]bool, len(prims))
	for _, p := range prims {
		combinesLeft[p.b] = true
	}
	nfcMaybe := coalesce(func(r rune) bool { return combinesLeft[r] })

	stats.tableBytes += len(cccT) + len(decompT) + len(composeT) +
		len(encodeRanges(nfcNo)) + len(encodeRanges(nfcMaybe)) + len(encodeRanges(nfdNo))

	gcbs, extPict, gcbVersion := parseGCBData()
	gcbTable, extT := encodeGCB(gcbs), encodeRanges(extPict)
	verifyGCB(gcbTable, extT, gcbs, extPict)
	stats.tableBytes += len(gcbTable) + len(extT)

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

	emitTable(&b, "_ccc_ranges",
		"Canonical combining classes: lo | hi | class. Only nonzero classes\n// are stored; everything absent is a starter (class 0).",
		cccT, len(cccs), cccChars)
	emitTable(&b, "_decomp_table",
		"Canonical decompositions, FULLY expanded and already in canonical\n// order: cp | to0 | to1 | to2 | to3, trailing 0 = absent. Storing the\n// recursive expansion rather than the one-step mapping means NFD needs\n// one lookup and no fixpoint loop. Hangul is absent by design — it\n// decomposes arithmetically.",
		decompT, len(decomps), decompChars)
	emitTable(&b, "_compose_table",
		"Primary composites, sorted by the PAIR: a | b | composed. This is\n// NOT the inverse of the table above — composition recombines one step\n// at a time, so the key is the one-step pair. Composition-excluded\n// characters are omitted, which is how Full_Composition_Exclusion is\n// represented here.",
		composeT, len(prims), composeChars)
	emitTable(&b, "_nfc_no_ranges",
		"Quick-check NFC=No: code points that never survive NFC unchanged.\n// Lets is_nfc reject without allocating a normalized copy.",
		encodeRanges(nfcNo), len(nfcNo), rangeChars)
	emitTable(&b, "_nfc_maybe_ranges",
		"Quick-check NFC=Maybe: marks that can combine with what precedes\n// them. Seeing one means the answer cannot be decided locally.",
		encodeRanges(nfcMaybe), len(nfcMaybe), rangeChars)
	emitTable(&b, "_nfd_no_ranges",
		"Quick-check NFD=No: code points that decompose, Hangul included.\n// Lets is_nfd answer without allocating.",
		encodeRanges(nfdNo), len(nfdNo), rangeChars)

	emitTable(&b, "_gcb_ranges",
		fmt.Sprintf("Grapheme_Cluster_Break (UAX #29, UCD %s): lo | hi | class.\n"+
			"// Class Other (0) is the default and is NOT stored -- it covers\n"+
			"// almost every code point, so storing it would bloat the table for\n"+
			"// no information. Class IDs are fixed by cmd/unicodegen and are\n"+
			"// part of this table's format.", gcbVersion),
		gcbTable, len(gcbs), gcbChars)
	emitTable(&b, "_extpict_ranges",
		"Extended_Pictographic ranges: lo | hi. Emoji, needed for the\n"+
			"// ZWJ-sequence rule (GB11) -- it is a separate property from the\n"+
			"// break classes above, so a code point can be both.",
		extT, len(extPict), rangeChars)

	b.WriteString(`// _dig decodes one table character to its 6-bit value. The alphabet
// skips ` + "`\\`" + ` (92), so the two spans are 48..91 and 93..112.
function _dig(c: i32): i32 {
    if (c <= 91) { return c - 48; }
    return c - 49;
}

// _fld reads the 24-bit field at character offset ` + "`i`" + `.
function _fld(t: string, i: i32): i32 {
    return (_dig(t[i] as i32) << 18) | (_dig(t[i + 1] as i32) << 12) | (_dig(t[i + 2] as i32) << 6) | _dig(t[i + 3] as i32);
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

// _upper_cp / _lower_cp are the i32 workers the rest of this module uses;
// the public surface is the ` + "`char`" + ` methods below. Keeping the internals on
// i32 avoids casting on every table lookup.
function _upper_cp(cp: i32): i32 {
    if (cp < 128) {
        if (cp >= 97 && cp <= 122) { return cp - 32; }
        return cp;
    }
    return _case_apply(cp, true);
}

function _lower_cp(cp: i32): i32 {
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
        var b: i32 = s[i] as i32;
        if (b >= from && b < from + 26) { b = b + (to - from); }
        buf = buf.with(i, b as u8);
        i = i + 1;
    }
    return string_from_bytes_unchecked(buf);
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
            var out: string = utf8.utf8_encode((_fld(t, base + 4)) as char);
            var c2: i32 = _fld(t, base + 8);
            if (c2 != 0) { out = out + utf8.utf8_encode((c2) as char); }
            var c3: i32 = _fld(t, base + 12);
            if (c3 != 0) { out = out + utf8.utf8_encode((c3) as char); }
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
        var st: i32 = utf8.floor_char_boundary(s, k - 1);
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
                    out = out + utf8.utf8_encode((_case_apply(pair.0, true)) as char);
                }
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode((65533) as char); i = i + 1; }
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
                    out = out + utf8.utf8_encode((962) as char);
                } else {
                    var full: string = _full_case(pair.0, false);
                    if (full.len() > 0) {
                        out = out + full;
                    } else {
                        out = out + utf8.utf8_encode((_case_apply(pair.0, false)) as char);
                    }
                }
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode((65533) as char); i = i + 1; }
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
            var out: string = utf8.utf8_encode((_fld(t, base + 4)) as char);
            var c2: i32 = _fld(t, base + 8);
            if (c2 != 0) { out = out + utf8.utf8_encode((c2) as char); }
            var c3: i32 = _fld(t, base + 12);
            if (c3 != 0) { out = out + utf8.utf8_encode((c3) as char); }
            return out;
        }
    }
    return utf8.utf8_encode((_lower_cp(cp)) as char);
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
            None => { out = out + utf8.utf8_encode((65533) as char); i = i + 1; }
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
            var ca: i32 = a[k] as i32;
            var cb: i32 = b[k] as i32;
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
    if (_is_upper_cp(cp)) { return _lower_cp(cp); }
    if (_is_lower_cp(cp)) { return _upper_cp(cp); }
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
            var b: i32 = s[k] as i32;
            if (b >= 65 && b <= 90) { b = b + 32; }
            else if (b >= 97 && b <= 122) { b = b - 32; }
            buf = buf.with(k, b as u8);
            k = k + 1;
        }
        return string_from_bytes_unchecked(buf);
    }
    var out: string = "";
    var len: i32 = s.len();
    var i: i32 = 0;
    while (i < len) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                out = out + utf8.utf8_encode((_swap_case_cp(pair.0)) as char);
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode((65533) as char); i = i + 1; }
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
            var up: i32 = _upper_cp(pair.0);
            if (up == pair.0) { return s; }
            return utf8.utf8_encode((up) as char) + s[pair.1:n];
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
                    out = out + utf8.utf8_encode((_upper_cp(cp)) as char);
                } else {
                    out = out + utf8.utf8_encode((cp) as char);
                }
                at_start = _is_whitespace_cp(cp);
                i = i + pair.1;
            },
            None => {
                out = out + utf8.utf8_encode((65533) as char);
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

// ` + "`_is_letter_cp(cp)`" + ` — is ` + "`cp`" + ` in the Unicode Letter category (L*)?
function _is_letter_cp(cp: i32): boolean {
    if (cp < 128) { return (cp >= 65 && cp <= 90) || (cp >= 97 && cp <= 122); }
    return _in_ranges(_letter_ranges(), cp);
}

// ` + "`_is_digit_cp(cp)`" + ` — is ` + "`cp`" + ` a Unicode decimal digit (category Nd)?
function _is_digit_cp(cp: i32): boolean {
    if (cp < 128) { return cp >= 48 && cp <= 57; }
    return _in_ranges(_digit_ranges(), cp);
}

// ` + "`_is_alnum_cp(cp)`" + ` — a letter or a decimal digit.
function _is_alnum_cp(cp: i32): boolean {
    return _is_letter_cp(cp) || _is_digit_cp(cp);
}

// ` + "`_is_whitespace_cp(cp)`" + ` — matches Go's unicode.IsSpace (space, tab,
// newline, NBSP, the Unicode space separators, …).
function _is_whitespace_cp(cp: i32): boolean {
    if (cp < 128) { return cp == 32 || (cp >= 9 && cp <= 13); }
    return _in_ranges(_space_ranges(), cp);
}

// ` + "`_is_upper_cp(cp)`" + ` / ` + "`_is_lower_cp(cp)`" + ` — Unicode upper/lowercase letters.
function _is_upper_cp(cp: i32): boolean {
    if (cp < 128) { return cp >= 65 && cp <= 90; }
    return _in_ranges(_upper_ranges(), cp);
}

function _is_lower_cp(cp: i32): boolean {
    if (cp < 128) { return cp >= 97 && cp <= 122; }
    return _in_ranges(_lower_ranges(), cp);
}


// === The ` + "`char`" + ` surface ===
//
// A ` + "`char`" + ` is a Unicode scalar value, distinct in the checker from the
// ` + "`i32`" + ` a byte rides in (#5629). These are METHODS rather than free
// functions because a free ` + "`to_upper(c: char)`" + ` would collide with
// ` + "`to_upper(s: string)`" + ` above — and because ` + "`c.to_upper()`" + ` next to
// ` + "`s.to_upper()`" + ` is exactly the point: the receiver TYPE says which of
// the two operations you meant, where before both were ` + "`i32`" + ` and only a
// naming convention told them apart.
//
// Case mapping here is SIMPLE (1:1). A 1->N expansion has no single
// ` + "`char`" + ` to return, so ` + "`'ß'`" + ` maps to itself; use the string-level
// ` + "`to_upper`" + ` when you need ` + "`SS`" + `. Rust draws the same line between
// ` + "`char::to_uppercase`" + ` and ` + "`str::to_uppercase`" + `.

// ` + "`c.to_upper()`" + ` — the simple uppercase of one scalar, or ` + "`c`" + ` unchanged.
pub function (c: char) to_upper(): char {
    return _upper_cp(c as i32) as char;
}

// ` + "`c.to_lower()`" + ` — the simple lowercase of one scalar.
pub function (c: char) to_lower(): char {
    return _lower_cp(c as i32) as char;
}

// ` + "`c.is_letter()`" + ` — is ` + "`c`" + ` in the Unicode Letter category (L*)?
pub function (c: char) is_letter(): boolean {
    return _is_letter_cp(c as i32);
}

// ` + "`c.is_digit()`" + ` — is ` + "`c`" + ` a Unicode decimal digit (category Nd)? Note
// this is the DECIMAL class, so it is true for Arabic-Indic and
// fullwidth digits, not just ASCII ` + "`0-9`" + `.
pub function (c: char) is_digit(): boolean {
    return _is_digit_cp(c as i32);
}

// ` + "`c.is_alnum()`" + ` — a letter or a decimal digit.
pub function (c: char) is_alnum(): boolean {
    return _is_alnum_cp(c as i32);
}

// ` + "`c.is_whitespace()`" + ` — matches Go's unicode.IsSpace (space, tab,
// newline, NBSP, the Unicode space separators, ...).
pub function (c: char) is_whitespace(): boolean {
    return _is_whitespace_cp(c as i32);
}

// ` + "`c.is_upper()`" + ` / ` + "`c.is_lower()`" + ` — Unicode upper/lowercase letters.
// Both are false for caseless scalars (digits, punctuation, CJK).
pub function (c: char) is_upper(): boolean {
    return _is_upper_cp(c as i32);
}

pub function (c: char) is_lower(): boolean {
    return _is_lower_cp(c as i32);
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
        if (kind == 0) { if (!_is_letter_cp(cp)) { return false; } }
        else if (kind == 1) { if (!_is_alnum_cp(cp)) { return false; } }
        else { if (!_is_digit_cp(cp)) { return false; } }
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

// _ccc_of is the canonical combining class of a code point: 0 for a
// starter, 1..254 for a combining mark. Nothing below U+0300 combines,
// which covers all of ASCII without a search.
function _ccc_of(cp: char): i32 {
    var n: i32 = cp as i32;
    if (n < 768) { return 0; }
    var t: string = _ccc_ranges();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 12;
        if (n < _fld(t, base)) { hi = mid - 1; }
        else if (n > _fld(t, base + 4)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

// _decomp_append appends the full canonical decomposition of cp to out,
// or cp itself when it does not decompose.
//
// Hangul is handled by the UAX #15 arithmetic rather than a lookup: the
// 11172 syllables would otherwise dominate the table, and getting the
// formula wrong is a classic normalization bug, so it is written out
// once here and pinned by tests.
function _decomp_append(out: char[], cp: char): char[] {
    var n: i32 = cp as i32;
    if (n >= 44032 && n < 55204) {
        var si: i32 = n - 44032;
        out = out.append((4352 + si / 588) as char);
        out = out.append((4449 + (si % 588) / 28) as char);
        var tj: i32 = si % 28;
        if (tj != 0) { out = out.append((4519 + tj) as char); }
        return out;
    }
    var t: string = _decomp_table();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 20 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 20;
        var from: i32 = _fld(t, base);
        if (n < from) {
            hi = mid - 1;
        } else if (n > from) {
            lo = mid + 1;
        } else {
            var k: i32 = 1;
            while (k <= 4) {
                var c: i32 = _fld(t, base + k * 4);
                if (c == 0) { return out; }
                out = out.append((c) as char);
                k = k + 1;
            }
            return out;
        }
    }
    return out.append(cp);
}

// _canon_order puts combining marks into canonical order: a stable
// insertion sort by combining class. Stability is required, not just
// nice — reordering marks of EQUAL class would change the text. It also
// runs linearly on already-ordered input, which is the common case.
function _canon_order(cps: char[]): char[] {
    var n: i32 = cps.len();
    var i: i32 = 1;
    while (i < n) {
        var c: i32 = _ccc_of(cps[i]);
        if (c != 0) {
            var j: i32 = i;
            while (j > 0) {
                // A starter has class 0, so this also stops the scan
                // dead at one: marks never migrate across a starter.
                if (_ccc_of(cps[j - 1]) <= c) { break; }
                var tmp: char = cps[j - 1];
                cps = cps.with(j - 1, cps[j]);
                cps = cps.with(j, tmp);
                j = j - 1;
            }
        }
        i = i + 1;
    }
    return cps;
}

// _compose_pair returns the primary composite of a + b, or 0 when the
// pair does not compose. U+0000 is never a composite, so it is an
// unambiguous "no".
function _compose_pair(a: char, b: char): i32 {
    var x: i32 = a as i32;
    var y: i32 = b as i32;
    // Hangul L + V, then LV + T — arithmetic again, matching _decomp_append.
    if (x >= 4352 && x < 4371 && y >= 4449 && y < 4470) {
        return 44032 + ((x - 4352) * 21 + (y - 4449)) * 28;
    }
    if (x >= 44032 && x < 55204 && y > 4519 && y < 4547) {
        if ((x - 44032) % 28 == 0) { return x + (y - 4519); }
        return 0;
    }
    var t: string = _compose_table();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 12;
        var ka: i32 = _fld(t, base);
        var kb: i32 = _fld(t, base + 4);
        if (x < ka || (x == ka && y < kb)) { hi = mid - 1; }
        else if (x > ka || (x == ka && y > kb)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

// _nfd_cps decomposes s into a canonically ordered code-point sequence.
function _nfd_cps(s: string): char[] {
    var cps: char[] = utf8.codepoints(s);
    var out: char[] = [];
    var i: i32 = 0;
    while (i < cps.len()) {
        out = _decomp_append(out, cps[i]);
        i = i + 1;
    }
    return _canon_order(out);
}

// _compose runs the NFC composition pass over a decomposed, canonically
// ordered sequence.
//
// The subtlety is BLOCKING: a mark can only combine with the last
// starter if nothing between them has a class greater than or equal to
// its own. prev_cc tracks the class of the character immediately
// preceding, with -1 meaning "nothing between", which is what lets two
// adjacent starters (Hangul L + V) compose.
function _compose(cps: char[]): char[] {
    var n: i32 = cps.len();
    if (n == 0) { return cps; }
    var out: char[] = [];
    out = out.append(cps[0]);
    var starter: i32 = -1;
    if (_ccc_of(cps[0]) == 0) { starter = 0; }
    var prev_cc: i32 = -1;
    var i: i32 = 1;
    while (i < n) {
        var cp: char = cps[i];
        var cc: i32 = _ccc_of(cp);
        var joined: boolean = false;
        if (starter >= 0 && (prev_cc == -1 || prev_cc < cc)) {
            var comp: i32 = _compose_pair(out[starter], cp);
            if (comp != 0) {
                out = out.with(starter, (comp) as char);
                joined = true;
            }
        }
        if (!joined) {
            if (cc == 0) {
                starter = out.len();
                prev_cc = -1;
            } else {
                prev_cc = cc;
            }
            out = out.append(cp);
        }
        i = i + 1;
    }
    return out;
}

// ` + "`nfd(s)`" + ` — canonical decomposition. Precomposed characters are split
// into base plus combining marks, and the marks are canonically ordered.
pub function nfd(s: string): string {
    if (_is_ascii(s)) { return s; }
    return utf8.encode_all(_nfd_cps(s));
}

// ` + "`nfc(s)`" + ` — canonical composition, the form to normalize to when in
// doubt. Text is decomposed first and then recombined, which is what
// makes NFC a true normal form rather than a best effort.
//
// Split from nfd deliberately: per-function DCE means a program that
// only ever calls nfd does not pay for the composition table.
pub function nfc(s: string): string {
    if (_is_ascii(s)) { return s; }
    return utf8.encode_all(_compose(_nfd_cps(s)));
}

// ` + "`is_nfd(s)`" + ` — is s already in NFD? Answers from a quick-check table
// without building a normalized copy, so the common "yes" costs one
// pass and no allocation.
pub function is_nfd(s: string): boolean {
    if (_is_ascii(s)) { return true; }
    var cps: char[] = utf8.codepoints(s);
    var prev_cc: i32 = 0;
    var i: i32 = 0;
    while (i < cps.len()) {
        var cp: char = cps[i];
        if (_in_ranges(_nfd_no_ranges(), cp as i32)) { return false; }
        var cc: i32 = _ccc_of(cp);
        if (cc != 0 && prev_cc > cc) { return false; }
        prev_cc = cc;
        i = i + 1;
    }
    return true;
}

// ` + "`is_nfc(s)`" + ` — is s already in NFC? The three-state quick check from
// UAX #15: a code point that never survives NFC answers No outright, a
// misordered mark likewise, and everything else answers Yes without
// allocating.
//
// The Maybe state is the one that cannot be shortcut. It is tempting to
// ask "does this mark compose with the starter before it?" and answer
// locally, but that is WRONG, because the preceding starter may itself
// decompose and the marks then reorder around it. U+1E63 followed by a
// cedilla is the counterexample: the pair does not compose, yet the
// string is not NFC, because U+1E63 splits into s + dot-below and the
// lower-class cedilla sorts in front of the dot, landing next to the s
// where it does compose. So a Maybe escalates to the full comparison.
//
// That fallback is why is_nfc saves ALLOCATION but not binary size: it
// reaches nfc, so it links the composition table. is_nfd has no such
// escalation and stays cheap on both counts.
pub function is_nfc(s: string): boolean {
    if (_is_ascii(s)) { return true; }
    var cps: char[] = utf8.codepoints(s);
    var prev_cc: i32 = 0;
    var i: i32 = 0;
    while (i < cps.len()) {
        var cp: char = cps[i];
        var n: i32 = cp as i32;
        var cc: i32 = _ccc_of(cp);
        if (cc != 0 && prev_cc > cc) { return false; }
        if (_in_ranges(_nfc_no_ranges(), n)) { return false; }
        if (_in_ranges(_nfc_maybe_ranges(), n)) { return nfc(s) == s; }
        prev_cc = cc;
        i = i + 1;
    }
    return true;
}

// ` + "`eq_canonical(a, b)`" + ` — are a and b the same text under canonical
// equivalence? This is what to reach for when comparing user-supplied
// text (search, dedup, usernames): NFC and NFD spellings of an accented
// character are equal here and NOT equal under ` + "`==`" + `.
//
// The byte-equality fast path means identical strings never normalize.
pub function eq_canonical(a: string, b: string): boolean {
    if (a == b) { return true; }
    return nfc(a) == nfc(b);
}

`)

	// The class IDs are substituted from cmd/unicodegen's own constants
	// rather than written out here, so the table encoder and the state
	// machine that reads it cannot drift apart.
	b.WriteString(strings.NewReplacer(
		"$CR", strconv.Itoa(gcbCR),
		"$LF", strconv.Itoa(gcbLF),
		"$CONTROL", strconv.Itoa(gcbControl),
		"$EXTEND", strconv.Itoa(gcbExtend),
		"$ZWJ", strconv.Itoa(gcbZWJ),
		"$RI", strconv.Itoa(gcbRegionalIndicator),
		"$PREPEND", strconv.Itoa(gcbPrepend),
		"$SPACINGMARK", strconv.Itoa(gcbSpacingMark),
		"$LVT", strconv.Itoa(gcbLVT),
		"$LV", strconv.Itoa(gcbLV),
		"$L", strconv.Itoa(gcbL),
		"$V", strconv.Itoa(gcbV),
		"$T", strconv.Itoa(gcbT),
	).Replace(`// _gcb_of is a code point's Grapheme_Cluster_Break class. Absent
// code points are Other, which is the overwhelming majority.
function _gcb_of(cp: char): i32 {
    var n: i32 = cp as i32;
    var t: string = _gcb_ranges();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 12;
        if (n < _fld(t, base)) { hi = mid - 1; }
        else if (n > _fld(t, base + 4)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

// _is_extpict — Extended_Pictographic, a property SEPARATE from the
// break classes: an emoji is usually class Other and pictographic.
function _is_extpict(cp: char): boolean {
    return _in_ranges(_extpict_ranges(), cp as i32);
}

// _gcb_break decides whether a cluster boundary falls between two
// adjacent code points, following the UAX #29 rules in their numbered
// order. The rules are order-sensitive: GB3 has to beat GB4, and the
// catch-all GB999 only applies once every joining rule has declined.
//
//   prev / cur  break classes of the two code points
//   ri          how many Regional_Indicators run up to prev
//   pict        1 = prev ends ExtPict Extend*, 2 = ... followed by ZWJ
//   cur_pict    is cur itself Extended_Pictographic
function _gcb_break(prev: i32, cur: i32, ri: i32, pict: i32, cur_pict: boolean): boolean {
    // GB3: CR x LF -- a Windows line ending is ONE cluster, not two.
    if (prev == $CR && cur == $LF) { return false; }
    // GB4 / GB5: a control, CR or LF never joins anything either way.
    if (prev == $CR || prev == $LF || prev == $CONTROL) { return true; }
    if (cur == $CR || cur == $LF || cur == $CONTROL) { return true; }
    // GB6 / GB7 / GB8: Hangul jamo compose into one syllable cluster.
    if (prev == $L && (cur == $L || cur == $V || cur == $LV || cur == $LVT)) { return false; }
    if ((prev == $LV || prev == $V) && (cur == $V || cur == $T)) { return false; }
    if ((prev == $LVT || prev == $T) && cur == $T) { return false; }
    // GB9 / GB9a: combining marks, ZWJ and spacing marks attach to what
    // precedes them -- this is the rule that keeps e + acute together.
    if (cur == $EXTEND || cur == $ZWJ) { return false; }
    if (cur == $SPACINGMARK) { return false; }
    // GB9b: Prepend attaches forwards instead.
    if (prev == $PREPEND) { return false; }
    // GB11: ExtPict Extend* ZWJ x ExtPict -- the emoji ZWJ sequences, so
    // a family emoji is one cluster rather than three people and a join.
    if (pict == 2 && cur_pict) { return false; }
    // GB12 / GB13: regional indicators pair up, so a flag is one cluster
    // and FOUR indicators are TWO flags rather than one long run.
    if (prev == $RI && cur == $RI && ri % 2 == 1) { return false; }
    // GB999: anything else breaks.
    return true;
}

// _pict_next advances the GB11 pictographic state.
function _pict_next(pict: i32, cls: i32, cur_pict: boolean): i32 {
    // A pictograph always (re)starts the sequence, including the one
    // that just terminated a ZWJ join.
    if (cur_pict) { return 1; }
    if (pict == 1 && cls == $EXTEND) { return 1; }
    if (pict == 1 && cls == $ZWJ) { return 2; }
    return 0;
}

// ` + "`graphemes(s)`" + ` — split into extended grapheme clusters: what a
// reader would call "characters". Combining sequences, emoji ZWJ
// sequences, flags and Hangul syllables each stay whole.
//
// Elements are ` + "`str`" + ` VIEWS into s, sliced by byte offset, so the split
// itself copies no text -- the clusters are contiguous runs of the
// input and materialising them as owned strings would be pure overhead.
//
// A WARNING WORTH HEEDING, and the reason this is opt-in: reaching for
// the n-th grapheme is usually a design smell. It is O(n) to find, and
// the answer is rarely what the problem actually needed. Prefer
// iterating this result, or an operation that does not need to know
// about clusters at all. Fern keeps ` + "`s.len()`" + ` in BYTES and ` + "`s[i]`" + ` a byte
// index precisely so that the cheap operations stay visibly cheap.
pub function graphemes(s: string): str[] {
    var out: str[] = [];
    var n: i32 = s.len();
    if (n == 0) { return out; }
    var start: i32 = 0;
    var i: i32 = 0;
    var prev: i32 = 0 - 1;
    var ri: i32 = 0;
    var pict: i32 = 0;
    while (i < n) {
        var cp: char = 0 as char;
        var w: i32 = 1;
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[i]) as char; w = 1; }
        }
        var cls: i32 = _gcb_of(cp);
        var cur_pict: boolean = _is_extpict(cp);
        if (prev >= 0) {
            if (_gcb_break(prev, cls, ri, pict, cur_pict)) {
                out = out.append(s[start : i]);
                start = i;
            }
        }
        if (cls == $RI) { ri = ri + 1; } else { ri = 0; }
        pict = _pict_next(pict, cls, cur_pict);
        prev = cls;
        i = i + w;
    }
    return out.append(s[start : n]);
}

// ` + "`grapheme_count(s)`" + ` — how many clusters, without building the array
// of them. Deliberately a separate scan rather than ` + "`graphemes(s).len()`" + `:
// counting should not allocate.
pub function grapheme_count(s: string): i32 {
    var n: i32 = s.len();
    if (n == 0) { return 0; }
    var count: i32 = 1;
    var i: i32 = 0;
    var prev: i32 = 0 - 1;
    var ri: i32 = 0;
    var pict: i32 = 0;
    while (i < n) {
        var cp: char = 0 as char;
        var w: i32 = 1;
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[i]) as char; w = 1; }
        }
        var cls: i32 = _gcb_of(cp);
        var cur_pict: boolean = _is_extpict(cp);
        if (prev >= 0) {
            if (_gcb_break(prev, cls, ri, pict, cur_pict)) { count = count + 1; }
        }
        if (cls == $RI) { ri = ri + 1; } else { ri = 0; }
        pict = _pict_next(pict, cls, cur_pict);
        prev = cls;
        i = i + w;
    }
    return count;
}

// ` + "`reverse_graphemes(s)`" + ` — reverse by cluster, which is the
// correct-by-default sibling of ` + "`reverse_bytes`" + `. An accented letter or a
// family emoji comes back intact rather than scrambled.
//
// ` + "`reverse_bytes`" + ` keeps its name and its place: it is the honest one,
// carrying the hazard in the name for callers who really do want bytes.
pub function reverse_graphemes(s: string): string {
    var gs: str[] = graphemes(s);
    var out: string = "";
    var i: i32 = gs.len() - 1;
    while (i >= 0) {
        out = out + gs[i];
        i = i - 1;
    }
    return out;
}
`))

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
