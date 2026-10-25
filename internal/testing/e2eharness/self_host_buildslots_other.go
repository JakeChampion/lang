//go:build !unix

package e2eharness

func acquireHostBuildSlot() func() { return func() {} }
