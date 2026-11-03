//go:build !computeracceptance || windows

package main

import "context"

// Normal/release builds do not contain an input automation listener.
func startComputerAcceptance(context.Context, *computerManager, []string) (func(), error) {
	return func() {}, nil
}
