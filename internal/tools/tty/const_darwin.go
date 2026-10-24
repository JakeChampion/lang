//go:build darwin

package tty

const tcGetAttr = 0x40487413

const tcSetAttr = 0x80487414

const tiocGWinSz = 0x40087468

const tiocSWinSz = 0x80087467
