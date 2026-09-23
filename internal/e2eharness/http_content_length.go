package e2eharness

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// HTTPContentLengthProbe compares public parser results with unbounded decimal
// arithmetic. Include lengths that wrap to zero or a small positive i32, since
// checking the sign after multiplication cannot reject those consistently.
func HTTPContentLengthProbe() string {
	var src strings.Builder
	src.WriteString(`import "std/http";
function parsed_length(value: string, body: string): i32 {
    var wire: string = "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: " + value + "\r\n\r\n" + body;
    match (http.http_parse_request(wire)) {
        Some(req) => { return req.body_len(); },
        None => { return -1; }
    }
}
function main(): i32 {
`)
	values := []string{"0", "1", "3", "16", "00000000000000000000000000000000000001", " 1\t",
		"1048576", "1048577", "2147483647", "2147483648", "4294967295", "4294967296",
		"4294967297", "4294967299", "8589934592", "12884901888", "17179869184",
		"42949672960", "18446744073709551616", strings.Repeat("9", 100), "", "+1", "-1", "1x", "1, 1",
		"1\r\nContent-Length: 1", "1\r\nTransfer-Encoding: chunked"}
	index := 0
	for _, value := range values {
		for _, available := range []int{0, 1, 3, 16} {
			index++
			want := -1
			decimal := strings.Trim(value, " \t")
			onlyDigits := decimal != "" && strings.Trim(decimal, "0123456789") == ""
			if n, ok := new(big.Int).SetString(decimal, 10); ok && onlyDigits && n.IsInt64() && n.Int64() <= int64(available) {
				want = int(n.Int64())
			}
			fmt.Fprintf(&src, "    if (parsed_length(%s, %s) != %d) { print(%q); return 1; }\n", strconv.Quote(value), strconv.Quote(strings.Repeat("x", available)), want, strconv.Itoa(index))
		}
	}
	src.WriteString(`    var full: string = "x".repeat(1048576);
    if (parsed_length("1048576", full) != 1048576) { print("body-limit"); return 1; }
    if (parsed_length("1048577", full + "x") != -1) { print("over-limit"); return 1; }
    return 42;
}
`)
	return src.String()
}
