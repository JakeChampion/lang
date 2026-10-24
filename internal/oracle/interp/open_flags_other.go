//go:build !unix

package interp

const (
	oNonblock  = 0
	oDirectory = 0
	oDsync     = 0
	oSync      = 0
	oNoctty    = 0
	oNofollow  = 0
)
