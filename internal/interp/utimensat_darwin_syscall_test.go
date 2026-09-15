//go:build darwin

package interp

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSetFileTimesDarwin(t *testing.T) {
	initial := [2]syscall.Timespec{{Sec: 1_600_000_000, Nsec: 123456789}, {Sec: 1_600_000_001, Nsec: 987654321}}
	explicit := [2]syscall.Timespec{{Sec: 1_650_000_000, Nsec: 111222333}, {Sec: 1_650_000_001, Nsec: 444555666}}
	omit := syscall.Timespec{Sec: -1, Nsec: utimeOmit}
	now := syscall.Timespec{Sec: -1, Nsec: utimeNow}
	for _, tc := range []struct {
		name  string
		times [2]syscall.Timespec
	}{
		{"explicit", explicit},
		{"omit-access", [2]syscall.Timespec{omit, explicit[1]}},
		{"omit-modification", [2]syscall.Timespec{explicit[0], omit}},
		{"omit-both", [2]syscall.Timespec{omit, omit}},
		{"now-access", [2]syscall.Timespec{now, explicit[1]}},
		{"now-modification", [2]syscall.Timespec{explicit[0], now}},
		{"now-and-omit", [2]syscall.Timespec{now, omit}},
		{"omit-and-now", [2]syscall.Timespec{omit, now}},
		{"now-both", [2]syscall.Timespec{now, now}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, nil, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := syscall.UtimesNano(path, initial[:]); err != nil {
				t.Fatal(err)
			}
			input := tc.times
			before := time.Now()
			if err := setFileTimes(path, &input, false); err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			if input != tc.times {
				t.Fatal("setFileTimes changed its input")
			}
			got := darwinFileTimes(t, path)
			for i, stamp := range tc.times {
				switch stamp.Nsec {
				case utimeOmit:
					stamp = initial[i]
				case utimeNow:
					actual := time.Unix(got[i].Sec, got[i].Nsec)
					if actual.Before(before) || actual.After(after) {
						t.Errorf("timestamp %d = %v, outside [%v, %v]", i, actual, before, after)
					}
					continue
				}
				if got[i] != stamp {
					t.Errorf("timestamp %d = %+v, want %+v", i, got[i], stamp)
				}
			}
			if tc.name == "now-both" && got[0] != got[1] {
				t.Errorf("now timestamps differ: %+v", got)
			}
		})
	}
}

func TestSetFileTimesDarwinSymlink(t *testing.T) {
	initial := [2]syscall.Timespec{{Sec: 1_600_000_000}, {Sec: 1_600_000_001}}
	updated := [2]syscall.Timespec{{Sec: 1_650_000_000, Nsec: 123456789}, {Sec: 1_650_000_001, Nsec: 987654321}}
	for _, nofollow := range []bool{false, true} {
		name := "follow"
		if nofollow {
			name = "nofollow"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := syscall.UtimesNano(target, initial[:]); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := setFileTimes(link, &updated, nofollow); err != nil {
				t.Fatal(err)
			}
			wantTarget := updated
			if nofollow {
				wantTarget = initial
				if got := darwinFileTimes(t, link); got != updated {
					t.Errorf("link times = %+v, want %+v", got, updated)
				}
			}
			if got := darwinFileTimes(t, target); got != wantTarget {
				t.Errorf("target times = %+v, want %+v", got, wantTarget)
			}
			if nofollow {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := setFileTimes(link, &initial, true); err != nil {
					t.Fatalf("dangling symlink: %v", err)
				}
				if got := darwinFileTimes(t, link); got != initial {
					t.Errorf("dangling link times = %+v, want %+v", got, initial)
				}
			}
		})
	}
}

func TestSetFileTimesDarwinErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	initial := darwinFileTimes(t, path)
	for _, tc := range []struct {
		name  string
		path  string
		times [2]syscall.Timespec
		want  error
	}{
		{"missing", path + ".missing", [2]syscall.Timespec{}, syscall.ENOENT},
		{"nul", path + "\x00", [2]syscall.Timespec{}, syscall.EINVAL},
		{"negative-access-nanoseconds", path, [2]syscall.Timespec{{Nsec: -1}, {}}, syscall.EINVAL},
		{"overflow-modification-nanoseconds", path, [2]syscall.Timespec{{}, {Nsec: 1_000_000_000}}, syscall.EINVAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := setFileTimes(tc.path, &tc.times, false); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
			if got := darwinFileTimes(t, path); got != initial {
				t.Errorf("failed call changed timestamps: %+v, want %+v", got, initial)
			}
		})
	}
}

func darwinFileTimes(t *testing.T, path string) [2]syscall.Timespec {
	t.Helper()
	var stat syscall.Stat_t
	if err := syscall.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return [2]syscall.Timespec{stat.Atimespec, stat.Mtimespec}
}
