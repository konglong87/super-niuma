package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"

	"github.com/konglong87/super-niuma/internal/media"
	_ "golang.org/x/image/webp"
)

// ReadArtifactContent returns model-ready bytes only after applying the same
// tenant/user/session policy as browser delivery and verifying stored content.
func (s *Service) ReadArtifactContent(ctx context.Context, req ArtifactContentRequest) (ArtifactContent, error) {
	if s == nil || s.cfg.MediaStore == nil || s.cfg.BlobStore == nil {
		return ArtifactContent{}, fmt.Errorf("%w: artifact content unavailable", ErrInvalidImageRequest)
	}
	if req.TenantID == 0 || req.UserID == 0 || req.SessionID == 0 || strings.TrimSpace(req.AssetID) == "" {
		return ArtifactContent{}, fmt.Errorf("%w: artifact scope and id are required", ErrInvalidImageRequest)
	}

	policy := media.AccessPolicy{TenantID: req.TenantID, UserID: req.UserID, SessionID: req.SessionID}
	asset, err := s.cfg.MediaStore.Get(ctx, policy, strings.TrimSpace(req.AssetID))
	if err != nil {
		return ArtifactContent{}, err
	}
	mediaType := normalizedImageMediaType(asset)
	if asset.State != media.StateReady || asset.Kind != media.KindImage || imageExtension(mediaType) == "" || strings.TrimSpace(asset.Original.Path) == "" {
		return ArtifactContent{}, fmt.Errorf("%w: artifact is not a readable image", ErrInvalidImageRequest)
	}

	reader, err := s.cfg.BlobStore.Open(ctx, policy, asset.Original.Path)
	if err != nil {
		return ArtifactContent{}, err
	}
	defer reader.Close()

	data, err := io.ReadAll(io.LimitReader(reader, s.cfg.MaxInputBytes+1))
	if err != nil {
		return ArtifactContent{}, err
	}
	if len(data) == 0 || int64(len(data)) > s.cfg.MaxInputBytes {
		return ArtifactContent{}, fmt.Errorf("%w: artifact content size is invalid", ErrInvalidImageRequest)
	}
	if err := verifyArtifactContent(asset, reader.Blob, mediaType, data); err != nil {
		return ArtifactContent{}, err
	}
	return ArtifactContent{MediaType: mediaType, Data: data}, nil
}

func normalizedImageMediaType(asset media.Asset) string {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(asset.Original.MediaType, ";")[0]))
	if mediaType == "" {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(asset.MediaType, ";")[0]))
	}
	return mediaType
}

func verifyArtifactContent(asset media.Asset, blob Blob, mediaType string, data []byte) error {
	actualSize := int64(len(data))
	for _, expected := range []int64{asset.SizeBytes, asset.Original.SizeBytes, blob.SizeBytes} {
		if expected > 0 && expected != actualSize {
			return fmt.Errorf("%w: artifact content size mismatch", ErrInvalidImageRequest)
		}
	}
	for _, storedType := range []string{asset.MediaType, asset.Original.MediaType, blob.MediaType} {
		normalized := strings.ToLower(strings.TrimSpace(strings.Split(storedType, ";")[0]))
		if normalized != "" && normalized != mediaType {
			return fmt.Errorf("%w: artifact media type mismatch", ErrInvalidImageRequest)
		}
	}

	digest := sha256.Sum256(data)
	actualDigest := hex.EncodeToString(digest[:])
	hasDigest := false
	for _, expected := range []string{asset.SHA256, asset.Original.SHA256, blob.SHA256} {
		expected = strings.ToLower(strings.TrimSpace(expected))
		if expected == "" {
			continue
		}
		hasDigest = true
		if expected != actualDigest {
			return fmt.Errorf("%w: artifact content checksum mismatch", ErrInvalidImageRequest)
		}
	}
	if !hasDigest {
		return fmt.Errorf("%w: artifact checksum unavailable", ErrInvalidImageRequest)
	}

	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || imageFormatMediaType(format) != mediaType {
		return fmt.Errorf("%w: artifact content is not the declared image type", ErrInvalidImageRequest)
	}
	return nil
}

func imageFormatMediaType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}
