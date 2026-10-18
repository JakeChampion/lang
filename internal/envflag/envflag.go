// Package envflag reads the boolean FERN_* environment variables by the one
// rule the compiler's std/envflag value_on also applies: unset, "", "0", "false"
// and "off", in any ASCII case, are off; anything else is on.
package envflag

import "os"

// ValueOn reports whether a switch set to v is on.
func ValueOn(v string) bool {
	return !(v == "" || v == "0" || equalFoldASCII(v, "false") || equalFoldASCII(v, "off"))
}

// On reports whether the environment switches name on.
func On(name string) bool {
	return ValueOn(os.Getenv(name))
}

// equalFoldASCII compares s with the lower-case ASCII word w, folding only
// A-Z, as std/string's eq_ignore_ascii_case does.
func equalFoldASCII(s, w string) bool {
	if len(s) != len(w) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != w[i] {
			return false
		}
	}
	return true
}
