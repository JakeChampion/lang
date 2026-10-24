package coreutils

func rawTimeFormats() []string {
	return []string{
		"\xff%Y\xfe", "\xe2\x82%Y\x80", "é€🙂%Y",
		"%é", "%5é", "%_8€", "%^8é", "%#8é",
		"%\xff", "%05\xff", "%^8\xffa", "%E\xff", "%:é",
	}
}
