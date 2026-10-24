//go:build linux

package tty

const tcGetAttr = 0x5401

const tcSetAttr = 0x5402

const tiocGWinSz = 0x5413

const tiocSWinSz = 0x5414
