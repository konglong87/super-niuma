package imagegen

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"
)

func imageRetryDelay(attempt uint) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func imageRetryJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	var sample [8]byte
	if _, err := rand.Read(sample[:]); err != nil {
		return base
	}
	window := uint64(base / 10)
	if window == 0 {
		return base
	}
	return base + time.Duration(binary.LittleEndian.Uint64(sample[:])%(window+1))
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
