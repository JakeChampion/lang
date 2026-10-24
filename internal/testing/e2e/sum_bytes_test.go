package e2e

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func sumBytesRef(s string) int32 {
	var sum uint32
	for i := 0; i < len(s); i++ {
		sum += uint32(s[i])
	}
	return int32(sum)
}

func sumBytesCases() []string {
	var out []string

	for n := 0; n <= 40; n++ {
		base := strings.Repeat("a", n)
		out = append(out, base, strings.Repeat("\xff", n))
		for at := 0; at < n; at++ {
			out = append(out, base[:at]+"\xff"+base[at+1:])
			out = append(out, base[:at]+"\x00"+base[at+1:])
		}
	}

	for _, n := range []int{31, 32, 33, 47, 48, 49, 63, 64, 65, 79, 80, 81} {
		base := strings.Repeat("a", n)
		out = append(out, base, strings.Repeat("\xff", n))
		for at := 0; at < n; at++ {
			out = append(out, base[:at]+"\xff"+base[at+1:])
		}
	}

	for _, n := range []int{15, 16, 17, 31, 32, 33, 47, 48, 64, 300} {
		var asc, hi strings.Builder
		for i := 0; i < n; i++ {
			asc.WriteByte(byte(i % 251))
			hi.WriteByte(byte(255 - i%251))
		}
		out = append(out, asc.String(), hi.String())
	}

	out = append(out,
		"",
		"\x00",
		"\x00\x00\x00\x00",
		"a\x00b",
		"\xff",
		"\x7f\x80",
		"\x80\x80\x80\x80",
		strings.Repeat("\xff", 300),
		strings.Repeat("\x00", 300),
		strings.Repeat("a", 255)+"\xff",
	)

	rng := rand.New(rand.NewSource(20260911))
	for i := 0; i < 60; i++ {
		n := rng.Intn(70)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(byte(rng.Intn(256)))
		}
		out = append(out, sb.String())
	}
	return out
}

func runSumBytesCorpus(t *testing.T, run func(t *testing.T, src string) string) {
	t.Helper()
	cases := sumBytesCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, s := range cases {
		body.WriteString(fmt.Sprintf("    write((__sum_bytes(%s)).to_string()); write(\"\\n\");\n",
			fernQuote(s)))
		want = append(want, fmt.Sprint(sumBytesRef(s)))
	}

	out := run(t, `import "std/i32";

function main(): i32 {
`+body.String()+`    return 0;
}
`)
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d — the program and the expectation "+
			"list are out of step, so no comparison below is trustworthy",
			len(got), len(want))
	}
	bad := 0
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("__sum_bytes(%q) = %s, want %s",
					cases[i], strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against the Go reference", len(want))
}

func TestX86_64SumBytes(t *testing.T) {
	runSumBytesCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunX86_64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestArm64SumBytes(t *testing.T) {
	runSumBytesCorpus(t, func(t *testing.T, src string) string {
		out, exit := compileAndRunArm64(t, src)
		if exit != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
		}
		return out
	})
}

func TestWASMSumBytes(t *testing.T) {
	runSumBytesCorpus(t, func(t *testing.T, src string) string {
		out, _ := invokeWasmtime(t, src)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if n := len(lines); n == 0 || strings.TrimSpace(lines[n-1]) != "0" {
			t.Fatalf("main() result line = %q, want \"0\"", lines[len(lines)-1])
		}
		return strings.Join(lines[:len(lines)-1], "\n")
	})
}

const sumBytesWrapSrc = `import "std/i32";

function main(): i32 {
    let big: string = "\u{FFFF}".repeat(6916212);
    write((__sum_bytes(big)).to_string());
    write("\n");
    return 0;
}
`

func TestX86_64SumBytesWraps(t *testing.T) {
	out, exit := compileAndRunX86_64(t, sumBytesWrapSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	if got := strings.TrimSpace(out); got != "356" {
		t.Errorf("__sum_bytes over 6916212 copies of U+FFFF = %s, want 356 "+
			"(the sum is 2^32 + 356, so anything else means the result "+
			"was not truncated to 32 bits)", got)
	}
}

func TestArm64SumBytesWraps(t *testing.T) {
	out, exit := compileAndRunArm64(t, sumBytesWrapSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	if got := strings.TrimSpace(out); got != "356" {
		t.Errorf("__sum_bytes over 6916212 copies of U+FFFF = %s, want 356 "+
			"(the sum is 2^32 + 356, so anything else means the result "+
			"was not truncated to 32 bits)", got)
	}
}

const sumBytesNoAllocSrc = `import "std/i32";

function main(): i32 {
    let s: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP";
    let before: i64 = __heap_bump_bytes();
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        total = total + __sum_bytes(s);
        i = i + 1;
    }
    let after: i64 = __heap_bump_bytes();
    write((after - before).to_string());
    write("\n");
    write(total.to_string());
    write("\n");
    return 0;
}
`

func TestX86_64SumBytesAllocatesNothing(t *testing.T) {
	out, exit := compileAndRunX86_64(t, sumBytesNoAllocSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	assertSumBytesNoAlloc(t, out)
}

func TestArm64SumBytesAllocatesNothing(t *testing.T) {
	out, exit := compileAndRunArm64(t, sumBytesNoAllocSrc)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	assertSumBytesNoAlloc(t, out)
}

func assertSumBytesNoAlloc(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two lines (bytes allocated, then the total), got %q", out)
	}
	if got := strings.TrimSpace(lines[0]); got != "0" {
		t.Errorf("1000 __sum_bytes calls moved the bump pointer by %s bytes, want 0", got)
	}
	want := fmt.Sprint(int32(1000 * int64(sumBytesRef("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP"))))
	if got := strings.TrimSpace(lines[1]); got != want {
		t.Errorf("accumulated total = %s, want %s — the loop did not actually run the kernel", got, want)
	}
}
