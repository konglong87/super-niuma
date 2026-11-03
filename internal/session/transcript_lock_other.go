//go:build !unix

package session

import "os"

func lockTranscriptFile(*os.File) error {
	return nil
}

func unlockTranscriptFile(*os.File) {}
