//go:build !darwin || !cgo

package macos

func RequestHostPermissions()            {}
func CheckHostPermissions() (bool, bool) { return true, true }
