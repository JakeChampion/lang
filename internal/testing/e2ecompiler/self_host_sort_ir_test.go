package e2ecompiler

import "testing"

const sortIRPrelude = `pub function sort_i32_asc(arr: i32[]): i32[] {
    let n: i32 = arr.len();
    if (n < 2) { return arr; }
    let src: i32[] = arr;
    let width: i32 = 1;
    while (width < n) {
        let dst: i32[] = [];
        let c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        let lo: i32 = 0;
        while (lo < n) {
            let mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            let hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            let i: i32 = lo;
            let j: i32 = mid;
            let k: i32 = lo;
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
    let n: i32 = arr.len();
    if (n < 2) { return arr; }
    let src: i32[] = arr;
    let width: i32 = 1;
    while (width < n) {
        let dst: i32[] = [];
        let c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        let lo: i32 = 0;
        while (lo < n) {
            let mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            let hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            let i: i32 = lo;
            let j: i32 = mid;
            let k: i32 = lo;
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
    let n: i32 = a.len();
    let m: i32 = b.len();
    let min: i32 = n;
    if (m < n) { min = m; }
    let i: i32 = 0;
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
    let n: i32 = arr.len();
    if (n < 2) { return arr; }
    let src: string[] = arr;
    let width: i32 = 1;
    while (width < n) {
        let dst: string[] = [];
        let c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        let lo: i32 = 0;
        while (lo < n) {
            let mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            let hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            let i: i32 = lo;
            let j: i32 = mid;
            let k: i32 = lo;
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
    let n: i32 = arr.len();
    if (n < 2) { return arr; }
    let src: string[] = arr;
    let width: i32 = 1;
    while (width < n) {
        let dst: string[] = [];
        let c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        let lo: i32 = 0;
        while (lo < n) {
            let mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            let hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            let i: i32 = lo;
            let j: i32 = mid;
            let k: i32 = lo;
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
    let i: i32 = 1;
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
	{"i32-asc", `let a: i32[] = [5, 3, 1, 4, 2]; let s: i32[] = sort_i32_asc(a); return s[0] * 10 + s[4];`, 15},
	{"i32-desc", `let a: i32[] = [5, 3, 1, 4, 2]; let s: i32[] = sort_i32_desc(a); return s[0] * 10 + s[4];`, 51},
	{"i32-sorted-pred", `let a: i32[] = [9, 1, 8, 2, 7, 3]; return is_sorted_i32_asc(sort_i32_asc(a));`, 1},
	{"i32-dups", `let a: i32[] = [4, 4, 1, 4]; let s: i32[] = sort_i32_asc(a); return s[1] + s[2];`, 8},
	{"string-cmp", `return string_cmp("apple", "banana") + 2;`, 1},
	{"string-asc", `let a: string[] = ["cherry", "apple", "banana"]; let s: string[] = sort_strings_asc(a); return s[0].len();`, 5},
	{"string-desc", `let a: string[] = ["cherry", "apple", "banana"]; let s: string[] = sort_strings_desc(a); return s[0].len();`, 6},
	{"i32-empty", `let e: i32[] = []; return sort_i32_asc(e).len() + 3;`, 3},
	{"string-singleton", `let a: string[] = ["hi"]; let s: string[] = sort_strings_asc(a); return s[0].len();`, 2},
}

func sortIRSrc(mainBody string) string {
	return sortIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
