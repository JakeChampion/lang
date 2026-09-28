package e2eharness

import "syscall"

// tcpCorkOpt holds a socket's writes in the kernel until it is uncorked or
// shut down, so the data and a half-close leave in one segment.
const tcpCorkOpt = syscall.TCP_CORK
