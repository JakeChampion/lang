package e2eharness

import "syscall"

// tcpCorkOpt holds a socket's writes in the kernel until it is shut down, so
// the data and a half-close leave together.
const tcpCorkOpt = syscall.TCP_NOPUSH
