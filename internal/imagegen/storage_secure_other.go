//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package imagegen

import (
	"context"

	"github.com/konglong87/super-niuma/internal/media"
)

func filesystemBlobStoreSecureSupported() bool { return false }

func filesystemPut(string, PutBlobRequest) (Blob, error) {
	return Blob{}, ErrFilesystemBlobStoreUnsupported
}

func filesystemOpen(string, media.AccessPolicy, string) (BlobReader, error) {
	return BlobReader{}, ErrFilesystemBlobStoreUnsupported
}

func filesystemDelete(string, media.AccessPolicy, string) error {
	return ErrFilesystemBlobStoreUnsupported
}

func filesystemListTenantBlobs(context.Context, string, uint64) ([]Blob, error) {
	return nil, ErrFilesystemBlobStoreUnsupported
}
