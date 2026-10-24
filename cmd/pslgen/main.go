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

const header = `// This module identifies public suffixes and registrable domain names.
//
// Generator: cmd/pslgen.
// Source: cmd/pslgen/public_suffix_list.dat, ICANN section.
// SHA-256: %x.
//
// Use ` + "`go run ./cmd/pslgen`" + ` to regenerate this file.
// Refresh the source list to include public suffix changes.

`

const body = `import "std/string";
import "std/array";

pub const COUNT: i32 = %d;

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

pub function is_public_suffix(host: string): boolean {
  let labels: string[] = __labels(host);
  return labels.len() > 0 && public_suffix(host) == __join(labels, 0);
}

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

function __rule(r: string): boolean {
  return __rules().contains("\n" + r + "\n");
}

function __rules(): string {
  return "`
