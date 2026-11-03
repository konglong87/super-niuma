package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"strings"

	"github.com/konglong87/super-niuma/internal/media"
)

func (e *Executor) persistOutput(ctx context.Context, job GenerationRecord, request normalizedJobRequest, output ProviderImage) (Artifact, error) {
	if len(output.Data) == 0 || int64(len(output.Data)) > e.cfg.MaxOutputBytes {
		return Artifact{}, fmt.Errorf("%w: provider returned invalid image size", ErrInvalidImageRequest)
	}
	mediaType := strings.ToLower(strings.TrimSpace(output.MediaType))
	if mediaType == "" {
		mediaType = "image/" + request.OutputFormat
	}
	extension := imageExtension(mediaType)
	if extension == "" {
		return Artifact{}, fmt.Errorf("%w: unsupported media type", ErrInvalidImageRequest)
	}
	assetID := deterministicImageAssetID(job.TenantID, job.GenerationID)
	key := fmt.Sprintf("tenant-%d/user-%d/session-%d/generation-%s.%s", job.TenantID, job.UserID, job.SessionID, job.GenerationID, extension)
	policy := media.AccessPolicy{TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID}
	blob, err := e.cfg.BlobStore.Put(ctx, PutBlobRequest{Policy: policy, Key: key, MediaType: mediaType, Name: assetID + "." + extension, Data: bytes.NewReader(output.Data)})
	if err != nil {
		return Artifact{}, err
	}
	width, height := 0, 0
	if config, _, err := image.DecodeConfig(bytes.NewReader(output.Data)); err == nil {
		width, height = config.Width, config.Height
	}
	asset := media.Asset{
		AssetID: assetID, Kind: media.KindImage, MediaType: mediaType, Name: blob.Name, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256, State: media.StateReady,
		TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID, Access: policy,
		Original: media.Variant{Path: key, URL: "/tenant/media/assets/" + assetID, MediaType: mediaType, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256},
	}
	if err := e.cfg.MediaStore.Put(ctx, asset); err != nil {
		return Artifact{}, err
	}
	now := e.cfg.Now().UTC()
	return Artifact{AssetID: assetID, GenerationID: job.GenerationID, TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID, Operation: job.Operation, SourceAssetID: job.SourceAssetID, MediaType: mediaType, Name: blob.Name, Width: width, Height: height, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256, URL: asset.Original.URL, Provider: job.Provider, Model: request.Model, CreatedAt: now}, nil
}

func (e *Executor) deleteCancelledBlob(ctx context.Context, job GenerationRecord, artifact Artifact) error {
	asset, err := e.cfg.MediaStore.Get(ctx, media.AccessPolicy{TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID}, artifact.AssetID)
	if err != nil && !errors.Is(err, media.ErrNotFound) {
		return err
	}
	key := fmt.Sprintf("tenant-%d/user-%d/session-%d/generation-%s.%s", job.TenantID, job.UserID, job.SessionID, job.GenerationID, imageExtension(artifact.MediaType))
	if asset.Original.Path != "" {
		key = asset.Original.Path
	}
	err = e.cfg.BlobStore.Delete(ctx, media.AccessPolicy{TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID}, key)
	if errors.Is(err, ErrBlobNotFound) {
		return nil
	}
	return err
}

func deterministicImageAssetID(tenantID uint64, generationID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", tenantID, generationID)))
	return "img-" + hex.EncodeToString(sum[:12])
}

func imageExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0])) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return ""
	}
}
