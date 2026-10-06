//go:build linux

package tty

// TCGETS.
const tcGetAttr = 0x5401

// TCSETS, and the two that follow it: TCSETSW applies the change once the
// output has drained, TCSETSF once it has drained and pending input is
// discarded. Consecutive, so SetTermios adds its action to this one.
const tcSetAttr = 0x5402

// TIOCGWINSZ.
const tiocGWinSz = 0x5413

// TIOCSWINSZ.
const tiocSWinSz = 0x5414
