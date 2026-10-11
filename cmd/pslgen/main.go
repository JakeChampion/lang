// Command pslgen generates internal/stdlib/std/publicsuffix.fern — the
// public suffix list's ICANN section — from the public_suffix_list.dat
// committed beside it.
//
// Usage, from the repository root:
//
//	go run ./cmd/pslgen
//
// To refresh the list: replace cmd/pslgen/public_suffix_list.dat with the
// current file from https://publicsuffix.org/list/public_suffix_list.dat
// and re-run; that is what `scripts/refresh-data publicsuffix` does, and
// what the refresh-data workflow does every week. TestCommittedFileIsUpToDate
// fails until the output is committed.
//
// Only the ICANN section is taken: the private section names hosting
// providers' domains (github.io, blogspot.com), which a cookie jar treats
// as one registrable domain each as browsers do, and which no mail or
// certificate rule needs; a program wanting them is a later flag here.
// A rule written in Unicode is bundled as its IDNA A-label (RFC 3492
// punycode under the `xn--` prefix), which is how a URL or a cookie's
// Domain attribute spells a name. The rules go into one string literal,
// not an array of them: a Fern array literal is code that builds the
// array on every call, a string literal is static data (cmd/unicodegen
// says more).
package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	listPath   = "cmd/pslgen/public_suffix_list.dat"
	outputPath = "internal/stdlib/std/publicsuffix.fern"
)

func main() {
	data, err := os.ReadFile(listPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, n, err := generate(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pslgen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputPath, []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d rules)\n", outputPath, n)
}

// icannRules reads the rules between the ICANN markers: one per line,
// the first field of it, comments and blank lines left out.
func icannRules(data []byte) ([]string, error) {
	var rules []string
	in := false
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "// ===BEGIN ICANN DOMAINS===":
			in = true
			continue
		case line == "// ===END ICANN DOMAINS===":
			in = false
			continue
		case !in || line == "" || strings.HasPrefix(line, "//"):
			continue
		}
		rule := strings.Fields(line)[0]
		ascii, err := alabel(rule)
		if err != nil {
			return nil, fmt.Errorf("line %d: %q: %v", i+1, rule, err)
		}
		rules = append(rules, ascii)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("no rules between the ICANN markers")
	}
	return rules, nil
}

// alabel is the rule with each label that is not ASCII punycoded, the
// `!` and `*` of an exception or wildcard rule kept, the whole in lower
// case.
func alabel(rule string) (string, error) {
	rule = strings.ToLower(rule)
	labels := strings.Split(rule, ".")
	for i, l := range labels {
		if l == "" {
			return "", fmt.Errorf("an empty label")
		}
		if isASCII(l) {
			continue
		}
		if !utf8.ValidString(l) {
			return "", fmt.Errorf("not UTF-8")
		}
		encoded, err := punycode(strings.TrimPrefix(l, "!"))
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(l, "!") {
			encoded = "!" + encoded
		}
		labels[i] = encoded
	}
	return strings.Join(labels, "."), nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// punycode is RFC 3492 §6.3, the label under the IDNA `xn--` prefix.
func punycode(label string) (string, error) {
	const (
		base, tmin, tmax = 36, 1, 26
		skew, damp       = 38, 700
		initialBias      = 72
		initialN         = 128
	)
	runes := []rune(label)
	var out []byte
	for _, r := range runes {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	n, delta, bias := initialN, 0, initialBias
	for h < len(runes) {
		m := int(utf8.MaxRune) + 1
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		if m-n > (1<<31-1-delta)/(h+1) {
			return "", fmt.Errorf("punycode overflow")
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tmin {
						t = tmin
					} else if t > tmax {
						t = tmax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return "xn--" + string(out), nil
}

// adapt is RFC 3492 §6.1's bias adaptation.
func adapt(delta, numPoints int, first bool) int {
	const base, tmin, tmax, skew, damp = 36, 1, 26, 38, 700
	if first {
		delta /= damp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	for delta > ((base-tmin)*tmax)/2 {
		delta /= base - tmin
		k += base
	}
	return k + (base-tmin+1)*delta/(delta+skew)
}

func digit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

// generate is the module's source for the list, and how many rules it
// holds.
func generate(data []byte) (string, int, error) {
	rules, err := icannRules(data)
	if err != nil {
		return "", 0, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, header, sha256.Sum256(data))
	fmt.Fprintf(&b, body, len(rules))
	b.WriteString(`\n`)
	for _, r := range rules {
		b.WriteString(r)
		b.WriteString(`\n`)
	}
	b.WriteString("\";\n}\n")
	return b.String(), len(rules), nil
}

const header = `// std/publicsuffix — the public suffix list (publicsuffix.org), the names
// under which the public may register a domain: ` + "`com`" + `, ` + "`co.uk`" + `, every
// label under ` + "`ck`" + ` but ` + "`www.ck`" + `. A cookie jar refuses a Domain that is one,
// since a cookie set for ` + "`co.uk`" + ` would reach every site under it; what else
// asks "is this one site or many" asks here. A program that imports the
// module carries the list, about 80 KB; one that does not pays nothing.
//
// GENERATED by cmd/pslgen from public_suffix_list.dat (sha256
// %x), its ICANN section only, each rule as its IDNA A-label.
// Do NOT edit by hand; re-run ` + "`go run ./cmd/pslgen`" + ` after refreshing
// cmd/pslgen/public_suffix_list.dat.
//
// The list goes stale: it is publicsuffix.org's on the day it was taken,
// so a suffix added since reads as a registrable domain until the file is
// regenerated.
//
// A rule matches a name that ends with its labels; ` + "`*.ck`" + ` matches any one
// label under ` + "`ck`" + `, and ` + "`!www.ck`" + ` exempts that one name from it. The public
// suffix is what the longest matching rule covers, an exception's parent
// before any other, and the last label alone when no rule matches.
`

const body = `
import "std/string";
import "std/array";

// COUNT is how many rules the list holds.
pub const COUNT: i32 = %d;

// public_suffix(host) — the public suffix of ` + "`host`" + `, an ASCII name as a
// URL or a cookie's Domain spells it, in any case, with or without one
// trailing dot: "" for a name with an empty label, otherwise never "".
pub function public_suffix(host: string): string {
  let labels: string[] = __labels(host);
  let i: i32 = 0;
  while (i < labels.len()) {
    let candidate: string = __join(labels, i);
    if (__rule("!" + candidate)) {
      return __join(labels, i + 1);
    }
    if (__rule(candidate) || i + 1 < labels.len() && __rule("*." + __join(labels, i + 1))) {
      return candidate;
    }
    i = i + 1;
  }
  if (labels.len() == 0) {
    return "";
  }
  return labels[labels.len() - 1];
}

// registrable_domain(host) — the public suffix and the one label before
// it, the name one registrant holds and the widest a cookie's Domain may
// name; None for a public suffix itself and for a name with an empty
// label.
pub function registrable_domain(host: string): Option[string] {
  let labels: string[] = __labels(host);
  let suffix: string = public_suffix(host);
  if (suffix.len() == 0) {
    return None;
  }
  let n: i32 = suffix.split(".").len();
  if (n >= labels.len()) {
    return None;
  }
  return Some(__join(labels, labels.len() - n - 1));
}

// is_public_suffix(host) — whether ` + "`host`" + ` is itself a public suffix.
pub function is_public_suffix(host: string): boolean {
  let labels: string[] = __labels(host);
  return labels.len() > 0 && public_suffix(host) == __join(labels, 0);
}

// __labels is the name's labels in lower case, one trailing dot
// dropped; none when a label is empty.
function __labels(host: string): string[] {
  let h: string = host.to_lower();
  if (h.ends_with(".")) {
    h = h.slice_snap(0, h.len() - 1);
  }
  let none: string[] = [];
  if (h.len() == 0) {
    return none;
  }
  let labels: string[] = h.split(".");
  for l in labels {
    if (l.len() == 0) {
      return none;
    }
  }
  return labels;
}

function __join(labels: string[], from: i32): string {
  return array.slice(labels, from, labels.len()).join(".");
}

// __rule is whether the list holds ` + "`r`" + ` as written.
function __rule(r: string): boolean {
  return __rules().contains("\n" + r + "\n");
}

// __rules is the list, one rule per line, a newline before the first and
// after the last.
function __rules(): string {
  return "`
