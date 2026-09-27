package stdlib

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/strerror"
)

// std/net carries its own errno numbers because a Fern module cannot read
// internal/strerror's table. This reads the module as data and pins every
// row to that table on each OS, and the variant order of `NetError` to the
// errno-name list, so neither copy can drift: a number corrected in one
// place fails here until the other follows.

const netSrc = "std/net.fern"

func readNet(t *testing.T) string {
	t.Helper()
	b, err := src.ReadFile(netSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", netSrc, err)
	}
	return string(b)
}

func netListBody(t *testing.T, s, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)function ` + name + `\(\): (?:string|i32)\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no %s() list in %s; the extraction pattern has gone stale, which would make this test vacuous", name, netSrc)
	}
	return m[1]
}

func netStrings(t *testing.T, s, name string) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(netListBody(t, s, name), -1) {
		out = append(out, m[1])
	}
	return out
}

func netInts(t *testing.T, s, name string) []int {
	t.Helper()
	var out []int
	for _, m := range regexp.MustCompile(`-?\d+`).FindAllString(netListBody(t, s, name), -1) {
		n, err := strconv.Atoi(m)
		if err != nil {
			t.Fatalf("%s(): %q is not an integer", name, m)
		}
		out = append(out, n)
	}
	return out
}

// The variant each errno name stands for, in the order both lists keep.
var netVariants = map[string]string{
	"EADDRINUSE":    "AddrInUse",
	"EADDRNOTAVAIL": "AddrNotAvailable",
	"ECONNREFUSED":  "ConnectionRefused",
	"ECONNRESET":    "ConnectionReset",
	"ECONNABORTED":  "ConnectionAborted",
	"ETIMEDOUT":     "TimedOut",
	"EAGAIN":        "WouldBlock",
	"EINPROGRESS":   "InProgress",
	"ENETUNREACH":   "NetworkUnreachable",
	"EHOSTUNREACH":  "HostUnreachable",
	"EINTR":         "Interrupted",
}

func TestNetErrnoTablesMatchStrerror(t *testing.T) {
	s := readNet(t)
	names := netStrings(t, s, "__net_errno_names")
	if len(names) != len(netVariants) {
		t.Fatalf("__net_errno_names has %d entries, want %d: %v", len(names), len(netVariants), names)
	}
	for os, list := range map[string]string{
		strerror.Linux:  "__net_errnos_linux",
		strerror.Darwin: "__net_errnos_darwin",
		strerror.Wasi:   "__net_errnos_wasi",
	} {
		nums := netInts(t, s, list)
		if len(nums) != len(names) {
			t.Fatalf("%s has %d entries, __net_errno_names has %d", list, len(nums), len(names))
		}
		for i, name := range names {
			want := strerror.Number(os, name)
			if want == 0 {
				t.Errorf("%s: %s has no number in internal/strerror", os, name)
				continue
			}
			if nums[i] != want {
				t.Errorf("%s[%d] = %d for %s, internal/strerror says %d", list, i, nums[i], name, want)
			}
		}
	}
	// EWOULDBLOCK must be EAGAIN's number everywhere, or `WouldBlock` would
	// miss one of the two spellings a non-blocking socket reports.
	for _, os := range []string{strerror.Linux, strerror.Darwin, strerror.Wasi} {
		if strerror.Number(os, "EWOULDBLOCK") != 0 && strerror.Number(os, "EWOULDBLOCK") != strerror.Number(os, "EAGAIN") {
			t.Errorf("%s: EWOULDBLOCK (%d) is not EAGAIN (%d)", os, strerror.Number(os, "EWOULDBLOCK"), strerror.Number(os, "EAGAIN"))
		}
	}
}

func TestNetErrorVariantsFollowErrnoList(t *testing.T) {
	s := readNet(t)
	m := regexp.MustCompile(`(?s)pub enum NetError \{(.*?)\}`).FindStringSubmatch(s)
	if m == nil {
		t.Fatal("no `pub enum NetError` in std/net.fern")
	}
	var variants []string
	for _, v := range strings.Split(m[1], ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if i := strings.Index(v, "("); i >= 0 {
			v = v[:i]
		}
		variants = append(variants, v)
	}
	names := netStrings(t, s, "__net_errno_names")
	if len(variants) != len(names)+1 || variants[len(variants)-1] != "Other" {
		t.Fatalf("NetError variants %v: want the %d named errnos then Other", variants, len(names))
	}
	for i, name := range names {
		if variants[i] != netVariants[name] {
			t.Errorf("NetError variant %d is %s, __net_errno_names[%d] is %s (%s)", i, variants[i], i, name, netVariants[name])
		}
	}
	// The index ladders that map between the two must name every variant
	// once each, in the same order.
	for i, name := range names {
		if !strings.Contains(s, "if (i == "+strconv.Itoa(i)+") { return "+strings.TrimPrefix(netVariants[name], "NetError.")) &&
			!strings.Contains(s, "if (i == "+strconv.Itoa(i)+") { return NetError."+netVariants[name]) {
			t.Errorf("__net_error_at has no arm returning %s at index %d", netVariants[name], i)
		}
		if !strings.Contains(s, netVariants[name]+" => { return "+strconv.Itoa(i)+"; }") {
			t.Errorf("__net_error_index has no arm mapping %s to %d", netVariants[name], i)
		}
	}
}
