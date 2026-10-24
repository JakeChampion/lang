package diag

var enableColor bool

func SetColor(on bool) bool {
	prev := enableColor
	enableColor = on
	return prev
}

func ColorEnabled() bool { return enableColor && !terse }

var useASCII bool

func SetASCII(on bool) bool {
	prev := useASCII
	useASCII = on
	return prev
}

func ASCIIEnabled() bool { return useASCII }

func boxVert() string {
	if useASCII {
		return "|"
	}
	return "│"
}

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiBlue   = "\x1b[34m"
	ansiBold   = "\x1b[1m"
	ansiRedBld = "\x1b[1;31m"
)

func paint(sgr, s string) string {
	if !enableColor || terse {
		return s
	}
	return sgr + s + ansiReset
}
