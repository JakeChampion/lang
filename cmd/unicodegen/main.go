package main

import (
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
)

const bias = 1 << 23

type fullCase struct {
	from rune
	to   [3]rune
}

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

const (
	fieldChars = 4
	recChars   = 5 * fieldChars
	rangeChars = 2 * fieldChars
	fullChars  = 4 * fieldChars
)

func enc6(d int) byte {
	if d < 44 {
		return byte(48 + d)
	}
	return byte(93 + d - 44)
}

func dec6(c byte) int {
	if c <= 91 {
		return int(c) - 48
	}
	return int(c) - 49
}

func encField(b *strings.Builder, v int) {
	if v < 0 || v >= 1<<24 {
		panic(fmt.Sprintf("unicodegen: field %d out of 24-bit range", v))
	}
	b.WriteByte(enc6((v >> 18) & 63))
	b.WriteByte(enc6((v >> 12) & 63))
	b.WriteByte(enc6((v >> 6) & 63))
	b.WriteByte(enc6(v & 63))
}

func decField(t string, i int) int {
	return dec6(t[i])<<18 | dec6(t[i+1])<<12 | dec6(t[i+2])<<6 | dec6(t[i+3])
}

type run struct {
	lo, hi rune
	kind   int
	du, dl int
}

func deltaUpper(r rune) int { return int(unicode.ToUpper(r)) - int(r) }
func deltaLower(r rune) int { return int(unicode.ToLower(r)) - int(r) }

func startsAlt(r rune) bool {
	return deltaUpper(r) == 0 && deltaLower(r) == 1
}

func altHolds(lo, cp rune) bool {
	if (cp-lo)&1 == 0 {
		return deltaUpper(cp) == 0 && deltaLower(cp) == 1
	}
	return deltaUpper(cp) == -1 && deltaLower(cp) == 0
}

const minAltRun = 4

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

func emitTable(b *strings.Builder, name, table string) {
	fmt.Fprintf(b, "function %s(): string {\n    return \"%s\";\n}\n\n", name, table)
}

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

var wordBreakMid = map[rune]bool{
	0x0027: true,
	0x002E: true,
	0x003A: true,
	0x00B7: true,
	0x0387: true,
	0x055F: true,
	0x05F4: true,
	0x2018: true,
	0x2019: true,
	0x2024: true,
	0x2027: true,
	0xFE13: true,
	0xFE52: true,
	0xFE55: true,
	0xFF07: true,
	0xFF0E: true,
	0xFF1A: true,
}

func isCased(r rune) bool {
	return unicode.Is(unicode.Lu, r) || unicode.Is(unicode.Ll, r) ||
		unicode.Is(unicode.Lt, r) ||
		unicode.Is(unicode.Other_Uppercase, r) ||
		unicode.Is(unicode.Other_Lowercase, r)
}

func isCaseIgnorable(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) ||
		unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Lm, r) ||
		unicode.Is(unicode.Sk, r) || wordBreakMid[r]
}

//go:embed normdata.txt
var normData string

type cccRun struct {
	lo, hi rune
	ccc    int
}

type decomp struct {
	cp rune
	to [4]rune
}

type primary struct {
	a, b, cp rune
}

const (
	cccChars     = 3 * fieldChars
	decompChars  = 5 * fieldChars
	composeChars = 3 * fieldChars
)

const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulTCount = 28
	hangulNCount = 588
	hangulSCount = 11172
)

func normDataVersion() string {
	for _, line := range strings.Split(normData, "\n") {
		if strings.HasPrefix(line, "# UCD ") {
			return strings.Fields(strings.TrimPrefix(line, "# UCD "))[0]
		}
	}
	panic("unicodegen: normdata.txt names no UCD version")
}

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

//go:embed gcbdata.txt
var gcbData string

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

var gcbNames = map[string]int{
	"CR": gcbCR, "LF": gcbLF, "CONTROL": gcbControl,
	"EXTEND": gcbExtend, "ZWJ": gcbZWJ,
	"REGIONAL_INDICATOR": gcbRegionalIndicator,
	"PREPEND":            gcbPrepend, "SPACINGMARK": gcbSpacingMark,
	"L": gcbL, "V": gcbV, "T": gcbT, "LV": gcbLV, "LVT": gcbLVT,
}

const gcbChars = 3 * fieldChars

type gcbRun struct {
	lo, hi rune
	class  int
}

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

//go:embed wbdata.txt
var wbData string

const (
	wbOther = iota
	wbCR
	wbLF
	wbNewline
	wbExtend
	wbZWJ
	wbRegionalIndicator
	wbFormat
	wbKatakana
	wbHebrewLetter
	wbALetter
	wbSingleQuote
	wbDoubleQuote
	wbMidNumLet
	wbMidLetter
	wbMidNum
	wbNumeric
	wbExtendNumLet
	wbWSegSpace
	wbClassCount
)

var wbNames = map[string]int{
	"CR": wbCR, "LF": wbLF, "NEWLINE": wbNewline,
	"EXTEND": wbExtend, "ZWJ": wbZWJ,
	"REGIONAL_INDICATOR": wbRegionalIndicator, "FORMAT": wbFormat,
	"KATAKANA": wbKatakana, "HEBREW_LETTER": wbHebrewLetter,
	"ALETTER": wbALetter, "SINGLE_QUOTE": wbSingleQuote,
	"DOUBLE_QUOTE": wbDoubleQuote, "MIDNUMLET": wbMidNumLet,
	"MIDLETTER": wbMidLetter, "MIDNUM": wbMidNum,
	"NUMERIC": wbNumeric, "EXTENDNUMLET": wbExtendNumLet,
	"WSEGSPACE": wbWSegSpace,
}

const wbChars = 3 * fieldChars

type wbRun struct {
	lo, hi rune
	class  int
}

func parseWBData() (wbs []wbRun, version string) {
	for _, line := range strings.Split(wbData, "\n") {
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
		if f[0] != "W" {
			panic("unicodegen: unknown wbdata record: " + line)
		}
		if len(f) != 4 {
			panic("unicodegen: malformed WB line: " + line)
		}
		class, ok := wbNames[f[3]]
		if !ok {
			panic("unicodegen: unknown Word_Break class: " + f[3])
		}
		wbs = append(wbs, wbRun{lo: rune(mustHex(f[1])), hi: rune(mustHex(f[2])), class: class})
	}
	sort.Slice(wbs, func(i, j int) bool { return wbs[i].lo < wbs[j].lo })
	return wbs, version
}

func encodeWB(rs []wbRun) string {
	var b strings.Builder
	for _, r := range rs {
		encField(&b, int(r.lo))
		encField(&b, int(r.hi))
		encField(&b, r.class)
	}
	return b.String()
}

func decodeWB(t string, cp int) int {
	lo, hi := 0, len(t)/wbChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * wbChars
		switch {
		case cp < decField(t, base):
			hi = mid - 1
		case cp > decField(t, base+fieldChars):
			lo = mid + 1
		default:
			return decField(t, base+2*fieldChars)
		}
	}
	return wbOther
}

func verifyWB(wbTable string, wbs []wbRun) {
	for i, r := range wbs {
		if r.lo > r.hi {
			panic(fmt.Sprintf("unicodegen: WB range U+%04X..U+%04X inverted", r.lo, r.hi))
		}
		if i > 0 && wbs[i-1].hi >= r.lo {
			panic(fmt.Sprintf("unicodegen: WB ranges overlap at U+%04X", r.lo))
		}
		if r.class <= wbOther || r.class >= wbClassCount {
			panic(fmt.Sprintf("unicodegen: WB class %d out of range at U+%04X", r.class, r.lo))
		}
		for _, cp := range []rune{r.lo, r.hi} {
			if got := decodeWB(wbTable, int(cp)); got != r.class {
				panic(fmt.Sprintf("unicodegen: wb(U+%04X) = %d, want %d", cp, got, r.class))
			}
		}
	}
	for _, tc := range []struct {
		cp    rune
		class int
	}{
		{0x000D, wbCR}, {0x000A, wbLF}, {0x000B, wbNewline},
		{0x0300, wbExtend}, {0x200D, wbZWJ}, {0x1F1E6, wbRegionalIndicator},
		{0x00AD, wbFormat}, {0x30A1, wbKatakana}, {0x05D0, wbHebrewLetter},
		{'a', wbALetter}, {'\'', wbSingleQuote}, {'"', wbDoubleQuote},
		{'.', wbMidNumLet}, {':', wbMidLetter}, {',', wbMidNum},
		{'0', wbNumeric}, {'_', wbExtendNumLet}, {' ', wbWSegSpace},
	} {
		if got := decodeWB(wbTable, int(tc.cp)); got != tc.class {
			panic(fmt.Sprintf("unicodegen: wb(U+%04X) = %d, want %d", tc.cp, got, tc.class))
		}
	}
}

type genStats struct{ runs, tableBytes int }

func graphemeScanner(signature, setup, empty, boundary, finish string) string {
	return fmt.Sprintf(`%s {
    %s
    let n: i32 = s.len();
    if (n == 0) { return %s; }
    let i: i32 = 0;
    let prev: i32 = 0 - 1;
    let ri: i32 = 0;
    let pict: i32 = 0;
    while (i < n) {
        let cp: char = 0 as char;
        let w: i32 = 1;
        match (utf8.decode_at(s, i)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[i]) as char; w = 1; }
        }
        let cls: i32 = _gcb_of(cp);
        let cur_pict: boolean = _is_extpict(cp);
        if (prev >= 0) {
            if (_gcb_break(prev, cls, ri, pict, cur_pict)) {
                %s
            }
        }
        if (cls == $RI) { ri = ri + 1; } else { ri = 0; }
        pict = _pict_next(pict, cls, cur_pict);
        prev = cls;
        i = i + w;
    }
    return %s;
}

`, signature, setup, empty, boundary, finish)
}

func generate() (string, genStats) {
	caseT, runs, classes := tables()
	stats := genStats{runs: len(runs), tableBytes: len(caseT)}
	for _, c := range classes {
		stats.tableBytes += len(c.table)
	}

	cccs, decomps, prims := parseNormData()
	cccT, decompT, composeT := encodeCCC(cccs), encodeDecomp(decomps), encodeCompose(prims)
	verifyNorm(cccT, decompT, composeT, cccs, decomps, prims)

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

	wbs, wbVersion := parseWBData()
	wbTable := encodeWB(wbs)
	verifyWB(wbTable, wbs)
	stats.tableBytes += len(wbTable)

	var b strings.Builder
	fmt.Fprintf(&b, `// This module converts Unicode case and classifies characters.
//
// Generator: cmd/unicodegen.
// Import this module for Unicode methods on strings and char values.
// std/string owns explicit ASCII transforms.
// Unicode versions: Go %s, normalization %s, grapheme boundaries %s, word boundaries %s.
// Use `+"`go run ./cmd/unicodegen internal/stdlib/std/unicode.fern`"+` to regenerate this file.

import "std/utf8" as utf8;

`, unicode.Version, normDataVersion(), strings.Fields(gcbVersion)[0], strings.Fields(wbVersion)[0])

	emitTable(&b, "_case_table", caseT)
	for _, name := range []string{"letter", "digit", "space", "upper", "lower", "cased", "caseignorable"} {
		c := classes[name]
		emitTable(&b, "_"+name+"_ranges", c.table)
	}
	emitTable(&b, "_full_upper_table", encodeFull(fullUpper))
	emitTable(&b, "_full_lower_table", encodeFull(fullLower))
	emitTable(&b, "_fold_table", encodeFull(foldExceptions))

	emitTable(&b, "_ccc_ranges", cccT)
	emitTable(&b, "_decomp_table", decompT)
	emitTable(&b, "_compose_table", composeT)
	emitTable(&b, "_nfc_no_ranges", encodeRanges(nfcNo))
	emitTable(&b, "_nfc_maybe_ranges", encodeRanges(nfcMaybe))
	emitTable(&b, "_nfd_no_ranges", encodeRanges(nfdNo))

	emitTable(&b, "_gcb_ranges", gcbTable)
	emitTable(&b, "_extpict_ranges", extT)

	emitTable(&b, "_wb_ranges", wbTable)

	b.WriteString(`function _dig(c: i32): i32 {
    if (c <= 91) { return c - 48; }
    return c - 49;
}

function _fld(t: string, i: i32): i32 {
    return (_dig(t[i] as i32) << 18) | (_dig(t[i + 1] as i32) << 12) | (_dig(t[i + 2] as i32) << 6) | _dig(t[i + 3] as i32);
}

function _case_apply(cp: i32, want_upper: boolean): i32 {
    let t: string = _case_table();
    let lo: i32 = 0;
    let hi: i32 = t.len() / 20 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 20;
        let rlo: i32 = _fld(t, base);
        if (cp < rlo) {
            hi = mid - 1;
        } else if (cp > _fld(t, base + 4)) {
            lo = mid + 1;
        } else {
            if (_fld(t, base + 8) == 0) {
                if (want_upper) { return cp + _fld(t, base + 12) - 8388608; }
                return cp + _fld(t, base + 16) - 8388608;
            }
            let odd: i32 = (cp - rlo) & 1;
            if (want_upper) { return cp - odd; }
            return cp + 1 - odd;
        }
    }
    return cp;
}

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

function _is_ascii(s: string): boolean {
    let n: i32 = s.len();
    let i: i32 = 0;
    while (i < n) {
        if (s[i] >= 128) { return false; }
        i = i + 1;
    }
    return true;
}

function _ascii_fold(s: string, from: i32, to: i32): string {
    let n: i32 = s.len();
    let first: i32 = 0;
    while (first < n && (s[first] as i32 < from || s[first] as i32 >= from + 26)) {
        first = first + 1;
    }
    if (first == n) {
        return s;
    }
    let buf: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) {
        let b: i32 = s[i] as i32;
        if (b >= from && b < from + 26) { b = b + (to - from); }
        buf = buf.with(i, b as u8);
        i = i + 1;
    }
    return string_from_bytes_unchecked(buf);
}

// _full_case_append appends the full case mapping of ` + "`cp`" + ` to the scalar buffer.
// The simple table applies when ` + "`cp`" + ` has no expansion.
// Binary search over 16-character records; a trailing 0 field means the
// expansion is shorter than 3 code points.
function _full_case_append(out: char[], cp: i32, want_upper: boolean): char[] {
    let t: string = _full_lower_table();
    if (want_upper) { t = _full_upper_table(); }
    let lo: i32 = 0;
    let hi: i32 = t.len() / 16 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 16;
        let from: i32 = _fld(t, base);
        if (cp < from) { hi = mid - 1; }
        else if (cp > from) { lo = mid + 1; }
        else {
            out = out.append(_fld(t, base + 4) as char);
            let c2: i32 = _fld(t, base + 8);
            if (c2 != 0) { out = out.append(c2 as char); }
            let c3: i32 = _fld(t, base + 12);
            if (c3 != 0) { out = out.append(c3 as char); }
            return out;
        }
    }
    return out.append(_case_apply(cp, want_upper) as char);
}

function _final_sigma(s: string, at: i32, sigma_len: i32): boolean {
    let before: boolean = false;
    let k: i32 = at;
    while (k > 0) {
        let st: i32 = utf8.floor_char_boundary(s, k - 1);
        let cp: i32 = 65533;
        match (utf8.decode_at(s, st)) {
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
    let n: i32 = s.len();
    let i: i32 = at + sigma_len;
    while (i < n) {
        let cp2: i32 = 65533;
        let w: i32 = 1;
        match (utf8.decode_at(s, i)) {
            Some(pr2) => { cp2 = pr2.0; w = pr2.1; },
            None => { }
        }
        if (_in_ranges(_caseignorable_ranges(), cp2)) { i = i + w; }
        else { return !_in_ranges(_cased_ranges(), cp2); }
    }
    return true;
}

// _map_upper and _map_lower decode ` + "`s`" + `, map every code point, and
// re-encode. Malformed bytes each become U+FFFD, with one U+FFFD per invalid byte.
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
    let out: char[] = [];
    let n: i32 = s.len();
    let i: i32 = 0;
    while (i < n) {
        match (utf8.decode_at(s, i)) {
            Some(pair) => {
                out = _full_case_append(out, pair.0, true);
                i = i + pair.1;
            },
            None => { out = out.append(65533 as char); i = i + 1; }
        }
    }
    return utf8.encode_all(out);
}

function _map_lower(s: string): string {
    let out: char[] = [];
    let n: i32 = s.len();
    let i: i32 = 0;
    while (i < n) {
        match (utf8.decode_at(s, i)) {
            Some(pair) => {
                if (pair.0 == 931 && _final_sigma(s, i, pair.1)) {
                    out = out.append(962 as char);
                } else {
                    out = _full_case_append(out, pair.0, false);
                }
                i = i + pair.1;
            },
            None => { out = out.append(65533 as char); i = i + 1; }
        }
    }
    return utf8.encode_all(out);
}

// ` + "`s.to_upper()`" + ` — ` + "`s`" + ` uppercased with FULL Unicode mapping, so
// a code point may expand to several (` + "`ß`" + ` -> ` + "`SS`" + `). Pure-ASCII input takes
// a byte fold and never touches a table.
pub function (s: string) to_upper(): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 97, 65); }
    return _map_upper(s);
}

// ` + "`s.to_lower()`" + ` — ` + "`s`" + ` lowercased with FULL Unicode mapping.
// Greek Final_Sigma IS applied: a word-final ` + "`Σ`" + ` becomes ` + "`ς`" + `, a
// medial one ` + "`σ`" + `.
pub function (s: string) to_lower(): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 65, 97); }
    return _map_lower(s);
}

// _fold_append appends the case-folded form of one code point.
// Case folding differs from lowercase conversion: ` + "`ß`" + `
// folds to ` + "`ss`" + ` and ` + "`ſ`" + ` folds to ` + "`s`" + `, neither of which the char ` + "`to_lower`" + `
// does. Only the differing code points are tabulated; the rest fall
// through to the simple lowercase mapping.
function _fold_append(out: char[], cp: i32): char[] {
    let t: string = _fold_table();
    let lo: i32 = 0;
    let hi: i32 = t.len() / 16 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 16;
        let from: i32 = _fld(t, base);
        if (cp < from) { hi = mid - 1; }
        else if (cp > from) { lo = mid + 1; }
        else {
            out = out.append(_fld(t, base + 4) as char);
            let c2: i32 = _fld(t, base + 8);
            if (c2 != 0) { out = out.append(c2 as char); }
            let c3: i32 = _fld(t, base + 12);
            if (c3 != 0) { out = out.append(c3 as char); }
            return out;
        }
    }
    return out.append(_lower_cp(cp) as char);
}

// ` + "`s.case_fold()`" + ` — ` + "`s`" + ` mapped to a form suitable for caseless comparison.
// NOT the same as lowercasing: folding ` + "`ß`" + ` gives ` + "`ss`" + `, so folding two
// strings and comparing catches equivalences ` + "`to_lower`" + ` misses. The
// result is for COMPARISON only — it is not meant to be displayed.
pub function (s: string) case_fold(): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 65, 97); }
    let out: char[] = [];
    let n: i32 = s.len();
    let i: i32 = 0;
    while (i < n) {
        match (utf8.decode_at(s, i)) {
            Some(pair) => {
                out = _fold_append(out, pair.0);
                i = i + pair.1;
            },
            None => { out = out.append(65533 as char); i = i + 1; }
        }
    }
    return utf8.encode_all(out);
}

// ` + "`a.eq_ignore_case(b)`" + ` — caseless equality under full case FOLDING, so
// ` + "`ß`" + ` and ` + "`ss`" + ` compare equal and ` + "`ſ`" + ` matches ` + "`s`" + `. Pure-ASCII
// operands stream byte-wise and allocate nothing; anything else folds
// both sides, because a fold can change length and so the two cannot be
// walked in lockstep.
pub function (a: string) eq_ignore_case(b: string): boolean {
    if (_is_ascii(a) && _is_ascii(b)) {
        let na: i32 = a.len();
        if (na != b.len()) { return false; }
        let k: i32 = 0;
        while (k < na) {
            let ca: i32 = a[k] as i32;
            let cb: i32 = b[k] as i32;
            if (ca >= 65 && ca <= 90) { ca = ca + 32; }
            if (cb >= 65 && cb <= 90) { cb = cb + 32; }
            if (ca != cb) { return false; }
            k = k + 1;
        }
        return true;
    }
    return a.case_fold() == b.case_fold();
}

function _swap_case_cp(cp: i32): i32 {
    if (_is_upper_cp(cp)) { return _lower_cp(cp); }
    if (_is_lower_cp(cp)) { return _upper_cp(cp); }
    return cp;
}

// ` + "`s.swap_case()`" + ` — every uppercase code point becomes lowercase and vice
// versa. Its own inverse on any string whose code points round-trip
// under simple mapping.
pub function (s: string) swap_case(): string {
    if (_is_ascii(s)) {
        let n: i32 = s.len();
        let buf: u8[] = __alloc_u8(n);
        let k: i32 = 0;
        while (k < n) {
            let b: i32 = s[k] as i32;
            if (b >= 65 && b <= 90) { b = b + 32; }
            else if (b >= 97 && b <= 122) { b = b - 32; }
            buf = buf.with(k, b as u8);
            k = k + 1;
        }
        return string_from_bytes_unchecked(buf);
    }
    let out: char[] = [];
    let len: i32 = s.len();
    let i: i32 = 0;
    while (i < len) {
        match (utf8.decode_at(s, i)) {
            Some(pair) => {
                out = out.append(_swap_case_cp(pair.0) as char);
                i = i + pair.1;
            },
            None => { out = out.append(65533 as char); i = i + 1; }
        }
    }
    return utf8.encode_all(out);
}

// ` + "`s.capitalize()`" + ` — uppercase the FIRST code point, leave the rest
// exactly as they are. Not Python's str.capitalize, which also
// lowercases the tail; preserving the tail is the less lossy default.
pub function (s: string) capitalize(): string {
    let n: i32 = s.len();
    if (n == 0) { return ""; }
    match (utf8.decode_at(s, 0)) {
        Some(pair) => {
            let up: i32 = _upper_cp(pair.0);
            if (up == pair.0) { return s; }
            return utf8.encode((up) as char) + slice_unchecked(s, pair.1, n);
        },
        None => { return s; }
    }
}

// ` + "`s.title_case()`" + ` — uppercase the first code point of every
// whitespace-separated word, leaving the rest of each word alone (so
// "FOX" stays "FOX"). Word breaks are any Unicode whitespace, not just
// U+0020 — the ASCII ` + "`to_ascii_title_case`" + ` breaks on the space byte
// only, so a tab-separated string titles differently between the two.
// This is a word-BREAK notion, not UAX #29 segmentation (#5633).
pub function (s: string) title_case(): string {
    let out: char[] = [];
    let n: i32 = s.len();
    let i: i32 = 0;
    let at_start: boolean = true;
    while (i < n) {
        match (utf8.decode_at(s, i)) {
            Some(pair) => {
                let cp: i32 = pair.0;
                let mapped: i32 = cp;
                if (at_start) { mapped = _upper_cp(cp); }
                out = out.append(mapped as char);
                at_start = _is_whitespace_cp(cp);
                i = i + pair.1;
            },
            None => {
                out = out.append(65533 as char);
                at_start = false;
                i = i + 1;
            }
        }
    }
    return utf8.encode_all(out);
}

function _in_ranges(t: string, cp: i32): boolean {
    let lo: i32 = 0;
    let hi: i32 = t.len() / 8 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 8;
        if (cp < _fld(t, base)) { hi = mid - 1; }
        else if (cp > _fld(t, base + 4)) { lo = mid + 1; }
        else { return true; }
    }
    return false;
}

function _is_letter_cp(cp: i32): boolean {
    if (cp < 128) { return (cp >= 65 && cp <= 90) || (cp >= 97 && cp <= 122); }
    return _in_ranges(_letter_ranges(), cp);
}

function _is_digit_cp(cp: i32): boolean {
    if (cp < 128) { return cp >= 48 && cp <= 57; }
    return _in_ranges(_digit_ranges(), cp);
}

function _is_alnum_cp(cp: i32): boolean {
    return _is_letter_cp(cp) || _is_digit_cp(cp);
}

function _is_whitespace_cp(cp: i32): boolean {
    if (cp < 128) { return cp == 32 || (cp >= 9 && cp <= 13); }
    return _in_ranges(_space_ranges(), cp);
}

function _is_upper_cp(cp: i32): boolean {
    if (cp < 128) { return cp >= 65 && cp <= 90; }
    return _in_ranges(_upper_ranges(), cp);
}

function _is_lower_cp(cp: i32): boolean {
    if (cp < 128) { return cp >= 97 && cp <= 122; }
    return _in_ranges(_lower_ranges(), cp);
}



pub function (c: char) to_upper(): char {
    return _upper_cp(c as i32) as char;
}

pub function (c: char) to_lower(): char {
    return _lower_cp(c as i32) as char;
}

pub function (c: char) is_letter(): boolean {
    return _is_letter_cp(c as i32);
}

pub function (c: char) is_digit(): boolean {
    return _is_digit_cp(c as i32);
}

// ` + "`c.is_alphanumeric()`" + ` — a letter or a decimal digit.
pub function (c: char) is_alphanumeric(): boolean {
    return _is_alnum_cp(c as i32);
}

pub function (c: char) is_whitespace(): boolean {
    return _is_whitespace_cp(c as i32);
}

pub function (c: char) is_upper(): boolean {
    return _is_upper_cp(c as i32);
}

pub function (c: char) is_lower(): boolean {
    return _is_lower_cp(c as i32);
}

function _all_cp(s: string, kind: i32): boolean {
    let n: i32 = s.len();
    if (n == 0) { return false; }
    let i: i32 = 0;
    while (i < n) {
        let cp: i32 = 65533;
        let w: i32 = 1;
        match (utf8.decode_at(s, i)) {
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

// ` + "`is_letters(s)`" + ` — every code point is a Unicode letter, so ` + "`élan`" + ` and
// ` + "`Ελλάδα`" + ` qualify where the byte-wise ASCII check rejects them.
pub function (s: string) is_letters(): boolean {
    return _all_cp(s, 0);
}

// ` + "`is_alphanumeric(s)`" + ` — every code point is a letter or a decimal digit.
pub function (s: string) is_alphanumeric(): boolean {
    return _all_cp(s, 1);
}

// ` + "`is_digits(s)`" + ` — every code point is a Unicode decimal digit (category
// Nd), which includes the Arabic-Indic and fullwidth digits, not just
// ASCII 0-9. Note this is a DIGIT test, not a number-parse test: it says
// nothing about signs, separators or overflow.
pub function (s: string) is_digits(): boolean {
    return _all_cp(s, 2);
}

function _ccc_of(cp: char): i32 {
    let n: i32 = cp as i32;
    if (n < 768) { return 0; }
    let t: string = _ccc_ranges();
    let lo: i32 = 0;
    let hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 12;
        if (n < _fld(t, base)) { hi = mid - 1; }
        else if (n > _fld(t, base + 4)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

function _decomp_append(out: char[], cp: char): char[] {
    let n: i32 = cp as i32;
    if (n >= 44032 && n < 55204) {
        let si: i32 = n - 44032;
        out = out.append((4352 + si / 588) as char);
        out = out.append((4449 + (si % 588) / 28) as char);
        let tj: i32 = si % 28;
        if (tj != 0) { out = out.append((4519 + tj) as char); }
        return out;
    }
    let t: string = _decomp_table();
    let lo: i32 = 0;
    let hi: i32 = t.len() / 20 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 20;
        let from: i32 = _fld(t, base);
        if (n < from) {
            hi = mid - 1;
        } else if (n > from) {
            lo = mid + 1;
        } else {
            let k: i32 = 1;
            while (k <= 4) {
                let c: i32 = _fld(t, base + k * 4);
                if (c == 0) { return out; }
                out = out.append((c) as char);
                k = k + 1;
            }
            return out;
        }
    }
    return out.append(cp);
}

function _canon_order(cps: char[]): char[] {
    let n: i32 = cps.len();
    let i: i32 = 1;
    while (i < n) {
        let c: i32 = _ccc_of(cps[i]);
        if (c != 0) {
            let j: i32 = i;
            while (j > 0) {
                if (_ccc_of(cps[j - 1]) <= c) { break; }
                let tmp: char = cps[j - 1];
                cps = cps.with(j - 1, cps[j]);
                cps = cps.with(j, tmp);
                j = j - 1;
            }
        }
        i = i + 1;
    }
    return cps;
}

function _compose_pair(a: char, b: char): i32 {
    let x: i32 = a as i32;
    let y: i32 = b as i32;
    if (x >= 4352 && x < 4371 && y >= 4449 && y < 4470) {
        return 44032 + ((x - 4352) * 21 + (y - 4449)) * 28;
    }
    if (x >= 44032 && x < 55204 && y > 4519 && y < 4547) {
        if ((x - 44032) % 28 == 0) { return x + (y - 4519); }
        return 0;
    }
    let t: string = _compose_table();
    let lo: i32 = 0;
    let hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 12;
        let ka: i32 = _fld(t, base);
        let kb: i32 = _fld(t, base + 4);
        if (x < ka || (x == ka && y < kb)) { hi = mid - 1; }
        else if (x > ka || (x == ka && y > kb)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

function _nfd_cps(s: string): char[] {
    let cps: char[] = (s).chars();
    let out: char[] = [];
    let i: i32 = 0;
    while (i < cps.len()) {
        out = _decomp_append(out, cps[i]);
        i = i + 1;
    }
    return _canon_order(out);
}

function _compose(cps: char[]): char[] {
    let n: i32 = cps.len();
    if (n == 0) { return cps; }
    let out: char[] = [];
    out = out.append(cps[0]);
    let starter: i32 = -1;
    if (_ccc_of(cps[0]) == 0) { starter = 0; }
    let prev_cc: i32 = -1;
    let i: i32 = 1;
    while (i < n) {
        let cp: char = cps[i];
        let cc: i32 = _ccc_of(cp);
        let joined: boolean = false;
        if (starter >= 0 && (prev_cc == -1 || prev_cc < cc)) {
            let comp: i32 = _compose_pair(out[starter], cp);
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

pub function nfd(s: string): string {
    if (_is_ascii(s)) { return s; }
    return utf8.encode_all(_nfd_cps(s));
}

pub function nfc(s: string): string {
    if (_is_ascii(s)) { return s; }
    return utf8.encode_all(_compose(_nfd_cps(s)));
}

pub function is_nfd(s: string): boolean {
    if (_is_ascii(s)) { return true; }
    let cps: char[] = (s).chars();
    let prev_cc: i32 = 0;
    let i: i32 = 0;
    while (i < cps.len()) {
        let cp: char = cps[i];
        if (_in_ranges(_nfd_no_ranges(), cp as i32)) { return false; }
        let cc: i32 = _ccc_of(cp);
        if (cc != 0 && prev_cc > cc) { return false; }
        prev_cc = cc;
        i = i + 1;
    }
    return true;
}

pub function is_nfc(s: string): boolean {
    if (_is_ascii(s)) { return true; }
    let cps: char[] = (s).chars();
    let prev_cc: i32 = 0;
    let i: i32 = 0;
    while (i < cps.len()) {
        let cp: char = cps[i];
        let n: i32 = cp as i32;
        let cc: i32 = _ccc_of(cp);
        if (cc != 0 && prev_cc > cc) { return false; }
        if (_in_ranges(_nfc_no_ranges(), n)) { return false; }
        if (_in_ranges(_nfc_maybe_ranges(), n)) { return nfc(s) == s; }
        prev_cc = cc;
        i = i + 1;
    }
    return true;
}

pub function eq_canonical(a: string, b: string): boolean {
    if (a == b) { return true; }
    return nfc(a) == nfc(b);
}

`)

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
	).Replace(`function _gcb_of(cp: char): i32 {
    let n: i32 = cp as i32;
    let t: string = _gcb_ranges();
    let lo: i32 = 0;
    let hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 12;
        if (n < _fld(t, base)) { hi = mid - 1; }
        else if (n > _fld(t, base + 4)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

function _is_extpict(cp: char): boolean {
    return _in_ranges(_extpict_ranges(), cp as i32);
}

function _gcb_break(prev: i32, cur: i32, ri: i32, pict: i32, cur_pict: boolean): boolean {
    if (prev == $CR && cur == $LF) { return false; }
    if (prev == $CR || prev == $LF || prev == $CONTROL) { return true; }
    if (cur == $CR || cur == $LF || cur == $CONTROL) { return true; }
    if (prev == $L && (cur == $L || cur == $V || cur == $LV || cur == $LVT)) { return false; }
    if ((prev == $LV || prev == $V) && (cur == $V || cur == $T)) { return false; }
    if ((prev == $LVT || prev == $T) && cur == $T) { return false; }
    if (cur == $EXTEND || cur == $ZWJ) { return false; }
    if (cur == $SPACINGMARK) { return false; }
    if (prev == $PREPEND) { return false; }
    if (pict == 2 && cur_pict) { return false; }
    if (prev == $RI && cur == $RI && ri % 2 == 1) { return false; }
    return true;
}

function _pict_next(pict: i32, cls: i32, cur_pict: boolean): i32 {
    if (cur_pict) { return 1; }
    if (pict == 1 && cls == $EXTEND) { return 1; }
    if (pict == 1 && cls == $ZWJ) { return 2; }
    return 0;
}

` + graphemeScanner("pub function graphemes(s: string): string[]", "let out: string[] = []; let start: i32 = 0;", "out", "out = out.append(slice_unchecked(s, start, i) + \"\"); start = i;", "out.append(slice_unchecked(s, start, n) + \"\")") +
		graphemeScanner("pub function grapheme_count(s: string): i32", "let out: i32 = 1;", "0", "out = out + 1;", "out") +
		graphemeScanner("function _grapheme_ends(s: string): i32[]", "let out: i32[] = [];", "out", "out = out.append(i);", "out.append(n)") + `
pub function reverse_graphemes(s: string): string {
    let n: i32 = s.len();
    if (n == 0) { return ""; }
    let ends: i32[] = _grapheme_ends(s);
    let bytes: u8[] = s.bytes();
    let h: usize = buf_new(n);
    let i: i32 = ends.len() - 1;
    while (i >= 0) {
        let start: i32 = 0;
        if (i > 0) { start = ends[i - 1]; }
        buf_push_bytes_range(h, bytes, start, ends[i]);
        i = i - 1;
    }
    let result: string = string_from_bytes_unchecked(buf_take_bytes(h));
    buf_free(h);
    return result;
}

function _wb_of(cp: char): i32 {
    let t: string = _wb_ranges();
    let n: i32 = cp as i32;
    let lo: i32 = 0;
    let hi: i32 = t.len() / 12 - 1;
    while (lo <= hi) {
        let mid: i32 = lo + (hi - lo) / 2;
        let base: i32 = mid * 12;
        if (n < _fld(t, base)) { hi = mid - 1; }
        else if (n > _fld(t, base + 4)) { lo = mid + 1; }
        else { return _fld(t, base + 8); }
    }
    return 0;
}

function _wb_ignorable(cls: i32): boolean {
    return cls == 4 || cls == 5 || cls == 7;
}

function _wb_needs_next(cls: i32): boolean {
    return cls == 11 || cls == 12 || cls == 13 || cls == 14 || cls == 15;
}

function _wb_next(s: string, i: i32): i32 {
    let n: i32 = s.len();
    let j: i32 = i;
    while (j < n) {
        let cp: char = 0 as char;
        let w: i32 = 1;
        match (utf8.decode_at(s, j)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[j]) as char; w = 1; }
        }
        let cls: i32 = _wb_of(cp);
        if (!_wb_ignorable(cls)) { return cls; }
        j = j + w;
    }
    return 0 - 1;
}

function _wb_break(prev2: i32, prev: i32, raw_prev: i32, cur: i32, next: i32, ri: i32, cur_pict: boolean): boolean {
    if (raw_prev == 1 && cur == 2) { return false; }
    if (raw_prev == 3 || raw_prev == 1 || raw_prev == 2) { return true; }
    if (cur == 3 || cur == 1 || cur == 2) { return true; }
    if (raw_prev == 5 && cur_pict) { return false; }
    if (raw_prev == 18 && cur == 18) { return false; }
    if (_wb_ignorable(cur)) { return false; }

    let ah_prev: boolean = prev == 10 || prev == 9;
    let ah_prev2: boolean = prev2 == 10 || prev2 == 9;
    let ah_cur: boolean = cur == 10 || cur == 9;
    let ah_next: boolean = next == 10 || next == 9;
    let mid_letter_q: boolean = cur == 14 || cur == 13 || cur == 11;
    let prev_mid_letter_q: boolean = prev == 14 || prev == 13 || prev == 11;
    let mid_num_q: boolean = cur == 15 || cur == 13 || cur == 11;
    let prev_mid_num_q: boolean = prev == 15 || prev == 13 || prev == 11;

    if (ah_prev && ah_cur) { return false; }
    if (ah_prev && mid_letter_q && ah_next) { return false; }
    if (ah_prev2 && prev_mid_letter_q && ah_cur) { return false; }
    if (prev == 9 && cur == 11) { return false; }
    if (prev == 9 && cur == 12 && next == 9) { return false; }
    if (prev2 == 9 && prev == 12 && cur == 9) { return false; }
    if (prev == 16 && cur == 16) { return false; }
    if (ah_prev && cur == 16) { return false; }
    if (prev == 16 && ah_cur) { return false; }
    if (prev2 == 16 && prev_mid_num_q && cur == 16) { return false; }
    if (prev == 16 && mid_num_q && next == 16) { return false; }
    if (prev == 8 && cur == 8) { return false; }
    if ((ah_prev || prev == 16 || prev == 8 || prev == 17) && cur == 17) { return false; }
    if (prev == 17 && (ah_cur || cur == 16 || cur == 8)) { return false; }
    if (prev == 6 && cur == 6 && ri % 2 == 1) { return false; }
    return true;
}

function _wb_is_word(s: string, a: i32, b: i32): boolean {
    let i: i32 = a;
    while (i < b) {
        let cp: char = 0 as char;
        let w: i32 = 1;
        match (utf8.decode_at(s, i)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[i]) as char; w = 1; }
        }
        let n: i32 = cp as i32;
        if (_is_letter_cp(n) || _is_digit_cp(n)) { return true; }
        i = i + w;
    }
    return false;
}

pub function word_segments(s: string): string[] {
    let out: string[] = [];
    let n: i32 = s.len();
    if (n == 0) { return out; }
    let start: i32 = 0;
    let i: i32 = 0;
    let prev2: i32 = 0 - 1;
    let prev: i32 = 0 - 1;
    let raw_prev: i32 = 0 - 1;
    let ri: i32 = 0;
    while (i < n) {
        let cp: char = 0 as char;
        let w: i32 = 1;
        match (utf8.decode_at(s, i)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[i]) as char; w = 1; }
        }
        let cls: i32 = _wb_of(cp);
        if (i > 0) {
            let nxt: i32 = 0 - 1;
            if (_wb_needs_next(cls)) { nxt = _wb_next(s, i + w); }
            if (_wb_break(prev2, prev, raw_prev, cls, nxt, ri, _is_extpict(cp))) {
                out = out.append((slice_unchecked(s, start, i) + ""));
                start = i;
            }
        }
        raw_prev = cls;
        if (!_wb_ignorable(cls)) {
            prev2 = prev;
            prev = cls;
            if (cls == 6) { ri = ri + 1; } else { ri = 0; }
        }
        i = i + w;
    }
    return out.append((slice_unchecked(s, start, n) + ""));
}

pub function words(s: string): string[] {
    let segs: string[] = word_segments(s);
    let out: string[] = [];
    let i: i32 = 0;
    let off: i32 = 0;
    while (i < segs.len()) {
        let w: i32 = segs[i].len();
        if (_wb_is_word(s, off, off + w)) { out = out.append(segs[i]); }
        off = off + w;
        i = i + 1;
    }
    return out;
}

pub function word_count(s: string): i32 {
    let n: i32 = s.len();
    if (n == 0) { return 0; }
    let count: i32 = 0;
    let start: i32 = 0;
    let i: i32 = 0;
    let prev2: i32 = 0 - 1;
    let prev: i32 = 0 - 1;
    let raw_prev: i32 = 0 - 1;
    let ri: i32 = 0;
    while (i < n) {
        let cp: char = 0 as char;
        let w: i32 = 1;
        match (utf8.decode_at(s, i)) {
            Some(pair) => { cp = (pair.0) as char; w = pair.1; },
            None => { cp = (s[i]) as char; w = 1; }
        }
        let cls: i32 = _wb_of(cp);
        if (i > 0) {
            let nxt: i32 = 0 - 1;
            if (_wb_needs_next(cls)) { nxt = _wb_next(s, i + w); }
            if (_wb_break(prev2, prev, raw_prev, cls, nxt, ri, _is_extpict(cp))) {
                if (_wb_is_word(s, start, i)) { count = count + 1; }
                start = i;
            }
        }
        raw_prev = cls;
        if (!_wb_ignorable(cls)) {
            prev2 = prev;
            prev = cls;
            if (cls == 6) { ri = ri + 1; } else { ri = 0; }
        }
        i = i + w;
    }
    if (_wb_is_word(s, start, n)) { count = count + 1; }
    return count;
}
`))

	src, err := fmtsource.Format(b.String())
	if err != nil {
		panic("unicodegen: generated source does not parse: " + err.Error())
	}
	return src, stats
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
