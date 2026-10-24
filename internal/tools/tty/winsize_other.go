//go:build !linux && !darwin

package tty

import "syscall"

func WindowSize(int) (int, int, error) { return 0, 0, syscall.ENOTTY }

func SetWindowSize(int, int, int) error { return syscall.ENOTTY }

func WindowSizeFull(int) (int, int, int, int, error) { return 0, 0, 0, 0, syscall.ENOTTY }

func SetWindowSizeFull(int, int, int, int, int) error { return syscall.ENOTTY }
