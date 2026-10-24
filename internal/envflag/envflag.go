package envflag

import "os"

func ValueOn(v string) bool {
	return !(v == "" || v == "0" || equalFoldASCII(v, "false") || equalFoldASCII(v, "off"))
}

func On(name string) bool {
	return ValueOn(os.Getenv(name))
}

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
