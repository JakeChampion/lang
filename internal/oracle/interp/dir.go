package interp

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// The directory handle. A Dir is a Struct whose `fd` names an *os.File in
// the open-file table, as a Reader's does, so `d.close()` is closeFile.
//
// The compiled runtime acts on a name through the descriptor (openat,
// fstatat, unlinkat). Go's syscall package has no *at calls on Darwin, so
// the interpreter resolves the name against the path the directory was
// opened by: the same answers, except that a walk through it is bounded
// by PATH_MAX where a compiled one is not.

// builtinOpenDir answers `open_dir(path)`: the directory, following a
// final symlink.
func builtinOpenDir(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("open_dir: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("open_dir: expected string path, got %T", args[0])
	}
	return openDirValue(i, string(path), string(path), false), nil
}

// builtinDirOpenDir answers `d.open_dir(name)`, which refuses a symlink
// rather than descending through it, as O_NOFOLLOW does.
func builtinDirOpenDir(i *Interp, args []Value) (Value, error) {
	path, name, err := dirName(i, "Dir.open_dir", 2, args)
	if err != nil {
		return nil, err
	}
	return openDirValue(i, path, name, true), nil
}

func openDirValue(i *Interp, path, name string, nofollow bool) Value {
	if nofollow {
		info, err := os.Lstat(path)
		if err != nil {
			return resultErr(classifyIoError(name, err))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return resultErr(classifyIoError(name, syscall.ELOOP))
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return resultErr(classifyIoError(name, err))
	}
	info, err := f.Stat()
	if err == nil && !info.IsDir() {
		err = syscall.ENOTDIR
	}
	if err != nil {
		f.Close()
		return resultErr(classifyIoError(name, err))
	}
	id := i.nextFd
	i.nextFd++
	i.openFiles[id] = f
	return resultOk(&Struct{TypeName: "Dir", Fields: map[string]Value{"fd": Number(id)}})
}

// dirName reads a Dir method's receiver and name: the path the name
// resolves to, and the name itself, which an Err carries. An absolute name
// ignores the directory, as it does under openat.
func dirName(i *Interp, what string, n int, args []Value) (string, string, error) {
	if len(args) != n {
		return "", "", fmt.Errorf("%s: expected %d args, got %d", what, n, len(args))
	}
	name, ok := args[1].(String)
	if !ok {
		return "", "", fmt.Errorf("%s: expected string name, got %T", what, args[1])
	}
	f, err := dirFile(i, args[0])
	if err != nil {
		return "", "", err
	}
	if f == nil || strings.HasPrefix(string(name), "/") {
		return string(name), string(name), nil
	}
	return f.Name() + "/" + string(name), string(name), nil
}

// dirFile is the *os.File behind a Dir, nil when it is closed.
func dirFile(i *Interp, v Value) (*os.File, error) {
	fd, err := streamFd(v)
	if err != nil {
		return nil, err
	}
	return i.openFiles[fd], nil
}

// closedDir is the Err a method on a closed Dir answers, as EBADF does.
func closedDir() Value {
	return resultErr(classifyIoError("", syscall.EBADF))
}

func builtinDirEntries(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("Dir.entries: expected 1 arg, got %d", len(args))
	}
	f, err := dirFile(i, args[0])
	if err != nil {
		return nil, err
	}
	if f == nil {
		return closedDir(), nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	out := newArray(len(entries))
	for k, e := range entries {
		out.E[k] = String(e.Name())
	}
	return resultOk(out), nil
}

// dirPathOp runs `op` on the path a Dir method's name resolves to.
func dirPathOp(i *Interp, what string, n int, args []Value, op func(path string) error) (Value, error) {
	path, name, err := dirName(i, what, n, args)
	if err != nil {
		return nil, err
	}
	if f, _ := dirFile(i, args[0]); f == nil {
		return closedDir(), nil
	}
	return ioResult(name, op(path)), nil
}

func builtinDirStat(follow bool) func(*Interp, []Value) (Value, error) {
	return func(i *Interp, args []Value) (Value, error) {
		path, name, err := dirName(i, "Dir.stat", 2, args)
		if err != nil {
			return nil, err
		}
		if f, _ := dirFile(i, args[0]); f == nil {
			return closedDir(), nil
		}
		resolve := os.Lstat
		if follow {
			resolve = os.Stat
		}
		info, err := resolve(path)
		if err != nil {
			return resultErr(classifyIoError(name, err)), nil
		}
		return resultOk(fileStatValue(info, statOrigin{path: path, follow: follow})), nil
	}
}

func builtinDirAccess(i *Interp, args []Value) (Value, error) {
	mode, err := numberArg("Dir.access", args, 2)
	if err != nil {
		return nil, err
	}
	return dirPathOp(i, "Dir.access", 3, args, func(p string) error { return accessEffective(p, int(mode)) })
}

func builtinDirRemove(dir bool) func(*Interp, []Value) (Value, error) {
	return func(i *Interp, args []Value) (Value, error) {
		return dirPathOp(i, "Dir.remove", 2, args, func(p string) error {
			if dir {
				return syscall.Rmdir(p)
			}
			return syscall.Unlink(p)
		})
	}
}

func builtinDirChmod(i *Interp, args []Value) (Value, error) {
	mode, err := numberArg("Dir.chmod", args, 2)
	if err != nil {
		return nil, err
	}
	follow, err := boolArg("Dir.chmod", args, 3)
	if err != nil {
		return nil, err
	}
	return dirPathOp(i, "Dir.chmod", 4, args, func(p string) error {
		return chmodAt(p, uint32(int(mode))&0o7777, follow)
	})
}

func builtinDirChown(i *Interp, args []Value) (Value, error) {
	uid, err := numberArg("Dir.chown", args, 2)
	if err != nil {
		return nil, err
	}
	gid, err := numberArg("Dir.chown", args, 3)
	if err != nil {
		return nil, err
	}
	follow, err := boolArg("Dir.chown", args, 4)
	if err != nil {
		return nil, err
	}
	// int32 first, so -1 stays the leave-alone sentinel (see builtinChownAt).
	return dirPathOp(i, "Dir.chown", 5, args, func(p string) error {
		return chownAt(p, int(int32(int64(uid))), int(int32(int64(gid))), follow)
	})
}

func numberArg(what string, args []Value, k int) (Number, error) {
	if k >= len(args) {
		return 0, fmt.Errorf("%s: expected an argument %d", what, k)
	}
	v, ok := args[k].(Number)
	if !ok {
		return 0, fmt.Errorf("%s: expected number arg %d, got %T", what, k, args[k])
	}
	return v, nil
}

func boolArg(what string, args []Value, k int) (bool, error) {
	if k >= len(args) {
		return false, fmt.Errorf("%s: expected an argument %d", what, k)
	}
	v, ok := args[k].(Bool)
	if !ok {
		return false, fmt.Errorf("%s: expected bool arg %d, got %T", what, k, args[k])
	}
	return bool(v), nil
}
