//go:build darwin

package tty

// TIOCGETA.
const tcGetAttr = 0x40487413

// TIOCGWINSZ.
const tiocGWinSz = 0x40087468

// TIOCSWINSZ.
const tiocSWinSz = 0x80087467
