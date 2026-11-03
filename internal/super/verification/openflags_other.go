//go:build !unix

package verification

// Non-Unix platforms fail closed until an equivalent no-follow/nonblocking
// open contract is implemented and tested; there is no weaker fallback.
const secureReadFlags = 0
const secureOpenSupported = false
