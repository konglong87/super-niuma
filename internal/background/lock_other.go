//go:build !unix

package background

import "os"

// No cross-process registry lock exists on non-unix platforms. Registry writes
// are still atomic there (write-temp-then-rename) and are still ordered within
// one process by withRegistryLock's mutex, so a reader never sees a half-written
// file; what is missing is exclusion between two niuma processes, which can
// still lose an update.
//
// This is a deliberate gap, not an oversight. Windows has no flock; LockFileEx
// is the equivalent and its semantics differ enough (mandatory rather than
// advisory, byte-range rather than whole-file) that a port needs to be tested on
// Windows to be trustworthy. This project is developed on darwin and tested on
// ubuntu, so such a port could not be verified here and a plausible-looking
// untested one would be worse than an honest gap. Tracked as TODO-120.
func lockFileExclusive(*os.File) error { return nil }

func unlockFile(*os.File) {}
