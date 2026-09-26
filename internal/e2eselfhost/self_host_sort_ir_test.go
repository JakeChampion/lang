package e2eselfhost

import "testing"

// sortIRCases exercise the merge-sort SHAPE through the self-host IR path on
// x86-64 + wasm. The per-width monomorphic sort family (`sort_i32_asc` etc.)
// retired to core/cmp's generic `sort` / `sort_desc` (#5397), but the relevant
// sort surface is inlined verbatim as monomorphic i32 / string merge sorts
// matching the fresh-copy shape `cmp.sort` / `cmp.sort_desc` and std/sort's
// `sort_by`-based string sorts now ship: the i32 ascending / descending stable
// bottom-up merge sorts (direct scalar compares), the byte-lexicographic
// `string_cmp` three-way comparator, and the string ascending / descending
// merge sorts built on it. This verifies the constructs those sorts lower to
// compile on the IR path: scalar (`i32[]`) and pointer (`string[]`) array build
// via `.append` (the per-pass scratch buffer is materialized FRESH — NOT
// aliased + CoW, which the self-host AST/non-IR paths mutate through in place;
// see core/cmp's `sort` doc comment and the #5397 AST-corruption finding),
// element rewrite via `.with`, indexed scalar + string-byte reads, `.len()`,
// numeric `<`/`>` comparisons, the nested `while` merge loops, and the n < 2
// return-borrowed-param early-out (executed by the i32-empty / string-singleton
// cases). Each program returns a small deterministic int (kept <= 126);
// expectations are oracle-checked against the native interpreter. FEATURE-AUDIT
// std/sort + core/cmp sort row.
const sortIRPrelude = `pub function sort_i32_asc(arr: i32[]): i32[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: i32[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: i32[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (src[j] < src[i]) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
pub function sort_i32_desc(arr: i32[]): i32[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: i32[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: i32[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (src[j] > src[i]) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
pub function string_cmp(a: string, b: string): i32 {
    var n: i32 = a.len();
    var m: i32 = b.len();
    var min: i32 = n;
    if (m < n) { min = m; }
    var i: i32 = 0;
    while (i < min) {
        if (a[i] < b[i]) { return 0 - 1; }
        if (a[i] > b[i]) { return 1; }
        i = i + 1;
    }
    if (n < m) { return 0 - 1; }
    if (n > m) { return 1; }
    return 0;
}
pub function sort_strings_asc(arr: string[]): string[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: string[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: string[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (string_cmp(src[j], src[i]) < 0) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
pub function sort_strings_desc(arr: string[]): string[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: string[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: string[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (string_cmp(src[j], src[i]) > 0) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
function is_sorted_i32_asc(a: i32[]): i32 {
    var i: i32 = 1;
    while (i < a.len()) {
        if (a[i - 1] > a[i]) { return 0; }
        i = i + 1;
    }
    return 1;
}
`

var sortIRCases = []struct {
	name string
	main string
	want int
}{
	// ascending merge sort: [5,3,1,4,2] -> [1,2,3,4,5]; min*10 + max = 15.
	{"i32-asc", `var a: i32[] = [5, 3, 1, 4, 2]; var s: i32[] = sort_i32_asc(a); return s[0] * 10 + s[4];`, 15},
	// descending: [5,3,1,4,2] -> [5,4,3,2,1]; first*10 + last = 51.
	{"i32-desc", `var a: i32[] = [5, 3, 1, 4, 2]; var s: i32[] = sort_i32_desc(a); return s[0] * 10 + s[4];`, 51},
	// the ascending result is non-decreasing -> predicate returns 1.
	{"i32-sorted-pred", `var a: i32[] = [9, 1, 8, 2, 7, 3]; return is_sorted_i32_asc(sort_i32_asc(a));`, 1},
	// duplicates survive (stable count): [4,4,1,4] -> [1,4,4,4]; middle two are 4 -> 8.
	{"i32-dups", `var a: i32[] = [4, 4, 1, 4]; var s: i32[] = sort_i32_asc(a); return s[1] + s[2];`, 8},
	// byte-lex comparator: "apple" < "banana" -> -1; mapped to a small int via +2.
	{"string-cmp", `return string_cmp("apple", "banana") + 2;`, 1},
	// string sort ascending: smallest is "apple" (len 5).
	{"string-asc", `var a: string[] = ["cherry", "apple", "banana"]; var s: string[] = sort_strings_asc(a); return s[0].len();`, 5},
	// string sort descending: largest is "cherry" (len 6).
	{"string-desc", `var a: string[] = ["cherry", "apple", "banana"]; var s: string[] = sort_strings_desc(a); return s[0].len();`, 6},
	// n < 2 early-return actually EXECUTES (returns the borrowed scalar
	// array param directly): empty -> len 0 (+3 to distinguish from traps).
	{"i32-empty", `var e: i32[] = []; return sort_i32_asc(e).len() + 3;`, 3},
	// n < 2 early-return over a pointer-element (string[]) array: the
	// singleton survives the return-borrowed-param path intact.
	{"string-singleton", `var a: string[] = ["hi"]; var s: string[] = sort_strings_asc(a); return s[0].len();`, 2},
}

func sortIRSrc(mainBody string) string {
	return sortIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostSortIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostSortIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range sortIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, sortIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
