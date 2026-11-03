//go:build !unix

package computerbridge

import "os"

const listenerUnixSupported = false

func listenerOwnedByUser(os.FileInfo) bool { return false }

func listenerTrustedAncestor(os.FileInfo) bool { return false }
