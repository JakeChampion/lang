package stdlib

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/strerror"
)

// std/net names the errno each NetError variant stands for as a std/errno
// constant, in variant order. This reads the module as data and pins that list
// to the variants and to internal/tables/strerror, so a variant added without
// its errno, or an errno the table does not carry, fails here.

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

// netErrnoNames is __net_errnos's list as the errno names it spells.
func netErrnoNames(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`errno\.(\w+)`).FindAllStringSubmatch(netListBody(t, s, "__net_errnos"), -1) {
		out = append(out, m[1])
	}
	return out
}

func TestNetErrnosMatchStrerror(t *testing.T) {
	names := netErrnoNames(t, readNet(t))
	if len(names) != len(netVariants) {
		t.Fatalf("__net_errnos has %d entries, want %d: %v", len(names), len(netVariants), names)
	}
	for _, name := range names {
		if _, ok := netVariants[name]; !ok {
			t.Errorf("__net_errnos names %s, which no NetError variant stands for", name)
		}
		if strerror.Number(strerror.Linux, name) == 0 {
			t.Errorf("__net_errnos names %s, which internal/tables/strerror has no Linux number for", name)
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
	names := netErrnoNames(t, s)
	if len(variants) != len(names)+1 || variants[len(variants)-1] != "Other" {
		t.Fatalf("NetError variants %v: want the %d named errnos then Other", variants, len(names))
	}
	for i, name := range names {
		if variants[i] != netVariants[name] {
			t.Errorf("NetError variant %d is %s, __net_errnos[%d] is %s (%s)", i, variants[i], i, name, netVariants[name])
		}
	}
	// The index ladders that map between the two must name every variant
	// once each, in the same order.
	for i, name := range names {
		v, n := regexp.QuoteMeta(netVariants[name]), strconv.Itoa(i)
		if !regexp.MustCompile(`if \(i == ` + n + `\) \{\s*return (NetError\.)?` + v + `\b`).MatchString(s) {
			t.Errorf("__net_error_at has no arm returning %s at index %d", netVariants[name], i)
		}
		if !regexp.MustCompile(`\b` + v + ` => \{\s*return ` + n + `;\s*\}`).MatchString(s) {
			t.Errorf("__net_error_index has no arm mapping %s to %d", netVariants[name], i)
		}
	}
}
