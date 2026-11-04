package interp

import (
	"errors"
	"fmt"
	"syscall"
)

func builtinWriterSetNonblocking(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Writer.set_nonblocking: expected 2 args")
	}
	on, ok := args[1].(Bool)
	if !ok {
		return nil, fmt.Errorf("Writer.set_nonblocking: the flag must be a boolean")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	fd, _ := streamFd(args[0])
	var serr error
	if _, owned := i.openFiles[fd]; owned {
		i.openFiles[fd], serr = hostReopenNonblock(f, bool(on))
	} else {
		serr = hostSetNonblock(f, bool(on))
	}
	if serr != nil {
		return resultErr(classifyIoError("", serr)), nil
	}
	return resultOk(unitValue()), nil
}
