package coreutils

// OS format arguments are byte strings. Unknown conversions and width
// flags can also divide a UTF-8 scalar into separate formatter pieces.
func rawTimeFormats() []string {
	return []string{
		"\xff%Y\xfe", "\xe2\x82%Y\x80", "é€🙂%Y",
		"%é", "%5é", "%_8€", "%^8é", "%#8é",
		"%\xff", "%05\xff", "%^8\xffa", "%E\xff", "%:é",
	}
}
