package interp

import "strings"

// rawMount is one row of `mounts()` before it becomes a MountEntry.
type rawMount struct {
	source, target, fstype string
	dev                    int64
}

// parseMountinfo reads Linux's /proc/self/mountinfo. A line is
//
//	ID PARENT MAJ:MIN ROOT POINT OPTIONS [TAG...] - TYPE SOURCE SUPEROPTS
//
// with a variable run of optional tags before the `-`, so the separator is
// searched for rather than counted to. A line too short to hold the fields is
// skipped.
func parseMountinfo(text string) []rawMount {
	var out []rawMount
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		sep := 6
		for sep < len(f) && f[sep] != "-" {
			sep++
		}
		if sep+2 >= len(f) {
			continue
		}
		out = append(out, rawMount{
			source: unescapeMountField(f[sep+2]),
			target: unescapeMountField(f[4]),
			fstype: unescapeMountField(f[sep+1]),
			dev:    parseMountDevno(f[2]),
		})
	}
	return out
}

// unescapeMountField undoes mountinfo's escaping: a space, tab, newline or
// backslash in a field is written as a backslash and three octal digits.
func unescapeMountField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// parseMountDevno turns mountinfo's `MAJ:MIN` into the kernel's st_dev
// encoding: the minor's low byte, the major above it, and the rest of the
// minor above the major.
func parseMountDevno(s string) int64 {
	var major, minor int64
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		major = major*10 + int64(s[i]-'0')
		i++
	}
	if i < len(s) && s[i] == ':' {
		i++
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		minor = minor*10 + int64(s[i]-'0')
		i++
	}
	return minor&0xff | major<<8 | (minor&^0xff)<<12
}
