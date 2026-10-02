package interp

import (
	"errors"
	"fmt"
	"syscall"
)

func builtinWriterWriteBytes(i *Interp, args []Value) (Value, error) {
	return writerBytes(i, args, true)
}

func builtinWriterWriteSomeBytes(i *Interp, args []Value) (Value, error) {
	return writerBytes(i, args, false)
}

// Borrow the Fern array. Copying into the host byte buffer keeps writes from
// exposing its mutable storage to an injected io.Writer.
func writerBytes(i *Interp, args []Value, all bool) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("byte write: expected 2 args")
	}
	a, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("byte write: content must be a byte array")
	}
	b := make([]byte, len(a.E))
	for n, v := range a.E {
		b[n] = byte(v.(Number))
	}
	failure := func(e error) Value {
		v := classifyIoError("", e)
		if all {
			return optionSome(v)
		}
		return resultErr(v)
	}
	fd, err := streamFd(args[0])
	if err != nil {
		return nil, err
	}
	if i.closedStd[fd] {
		return failure(syscall.EBADF), nil
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return failure(syscall.EBADF), nil
	}
	if err != nil {
		return nil, err
	}
	var write func([]byte) (int, error)
	if f != nil {
		write = func(p []byte) (int, error) { return syscall.Write(int(f.Fd()), p) }
	} else {
		w, e := writerStream(i, args[0])
		if e != nil {
			return nil, e
		}
		write = w.Write
	}
	for {
		n, e := write(b)
		if e != nil {
			return failure(e), nil
		}
		if n < 0 || n > len(b) {
			return failure(syscall.EIO), nil
		}
		if !all {
			return resultOk(Number(n)), nil
		}
		if n == len(b) {
			return optionNone(), nil
		}
		if n == 0 {
			return failure(syscall.EIO), nil
		}
		b = b[n:]
	}
}
