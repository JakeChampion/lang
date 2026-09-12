//go:build linux

package tty

// TCGETS.
const tcGetAttr = 0x5401

// TIOCGWINSZ.
const tiocGWinSz = 0x5413

// TIOCSWINSZ.
const tiocSWinSz = 0x5414
