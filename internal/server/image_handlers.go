package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// ImageGenerationHistoryStore is intentionally narrower than the MySQL
// repository so alternate persistence backends can be injected in tests and
// deployments without coupling handlers to GORM.
type ImageGenerationHistoryStore interface {
	ListImageGenerations(context.Context, uint64, uint64, uint64, int) ([]imagegen.GenerationRecord, error)
}

type imageGenerateRequest struct {
	Prompt         string `json:"prompt"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Size           string `json:"size,omitempty"`
	Resolution     string `json:"resolution,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"`
	Background     string `json:"background,omitempty"`
	Watermark      *bool  `json:"watermark,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func tenantImageGenerateHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.ImageGenerator == nil {
			writeImageError(w, imagegen.ErrImageGenerationDisabled)
			return
		}
		ctx, sessionID, ok := resolveImageSession(w, r, opts)
		if !ok {
			return
		}
		var req imageGenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		resolved, err := opts.TenantService.ResolveContext(ctx)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		artifact, err := opts.ImageGenerator.Generate(ctx, imagegen.GenerateRequest{TenantID: resolved.TenantID, UserID: resolved.UserID, SessionID: sessionID, Provider: req.Provider, Prompt: req.Prompt, Model: req.Model, Quality: req.Quality, Size: req.Size, Resolution: req.Resolution, AspectRatio: req.AspectRatio, OutputFormat: req.OutputFormat, Background: req.Background, Watermark: req.Watermark, IdempotencyKey: firstImageValue(r.Header.Get("Idempotency-Key"), req.IdempotencyKey)})
		if err != nil {
			writeImageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"asset": artifact})
	})
}

func tenantImageEditHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.ImageGenerator == nil {
			writeImageError(w, imagegen.ErrImageGenerationDisabled)
			return
		}
		ctx, sessionID, ok := resolveImageSession(w, r, opts)
		if !ok {
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeTenantError(w, http.StatusBadRequest, "invalid multipart form")
			return
		}
		prompt := strings.TrimSpace(r.FormValue("prompt"))
		var src io.Reader
		var srcName, srcType string
		if file, header, err := r.FormFile("image"); err == nil {
			src, srcName, srcType = file, header.Filename, header.Header.Get("Content-Type")
			defer file.Close()
		} else if !errors.Is(err, http.ErrMissingFile) {
			writeTenantError(w, http.StatusBadRequest, "invalid image upload")
			return
		}
		var mask io.Reader
		var maskName, maskType string
		if file, header, err := r.FormFile("mask"); err == nil {
			mask, maskName, maskType = file, header.Filename, header.Header.Get("Content-Type")
			defer file.Close()
		} else if !errors.Is(err, http.ErrMissingFile) {
			writeTenantError(w, http.StatusBadRequest, "invalid mask upload")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(ctx)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		var watermark *bool
		if value := strings.TrimSpace(r.FormValue("watermark")); value != "" {
			parsed := strings.EqualFold(value, "true")
			watermark = &parsed
		}
		artifact, err := opts.ImageGenerator.Edit(ctx, imagegen.EditRequest{TenantID: resolved.TenantID, UserID: resolved.UserID, SessionID: sessionID, Provider: r.FormValue("provider"), Prompt: prompt, Model: r.FormValue("model"), Quality: r.FormValue("quality"), Size: r.FormValue("size"), Resolution: r.FormValue("resolution"), AspectRatio: r.FormValue("aspect_ratio"), OutputFormat: r.FormValue("output_format"), Background: r.FormValue("background"), Watermark: watermark, SourceAssetID: strings.TrimSpace(r.FormValue("source_asset_id")), SourceName: srcName, SourceType: srcType, Source: src, Mask: mask, MaskName: maskName, MaskType: maskType, IdempotencyKey: firstImageValue(r.Header.Get("Idempotency-Key"), r.FormValue("idempotency_key"))})
		if err != nil {
			writeImageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"asset": artifact})
	})
}

func tenantImageCapabilitiesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		catalog := make([]map[string]any, 0, len(opts.ImageCatalog))
		for _, entry := range opts.ImageCatalog {
			catalog = append(catalog, map[string]any{
				"provider":       entry.Provider,
				"model":          entry.Model,
				"label":          entry.Label,
				"image_protocol": entry.ImageProtocol,
				"capability":     entry.Capability,
			})
		}
		writeJSON(w, map[string]any{"data": catalog})
	})
}

func tenantImageHistoryHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, sessionID, ok := resolveImageSession(w, r, opts)
		if !ok {
			return
		}
		if opts.ImageGenerationStore == nil {
			writeJSON(w, map[string]any{"data": []imagegen.Artifact{}})
			return
		}
		resolved, err := opts.TenantService.ResolveContext(ctx)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		rows, err := opts.ImageGenerationStore.ListImageGenerations(ctx, resolved.TenantID, resolved.UserID, sessionID, parseLimit(r.URL.Query().Get(paramLimit)))
		if err != nil {
			writeImageError(w, err)
			return
		}
		out := make([]imagegen.GenerationRecord, 0, len(rows))
		for _, row := range rows {
			if row.Status != imagegen.GenerationStatusCompleted || strings.TrimSpace(row.AssetID) == "" {
				continue
			}
			out = append(out, row)
		}
		writeJSON(w, map[string]any{"data": out})
	})
}

func tenantImageAssetHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.MediaAssetStore == nil || opts.ImageBlobStore == nil {
			writeImageError(w, imagegen.ErrImageGenerationDisabled)
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		assetID := path.Base(r.URL.Path)
		if assetID == "." || assetID == "/" || strings.TrimSpace(assetID) == "" {
			writeTenantError(w, http.StatusBadRequest, "asset id is required")
			return
		}
		asset, err := opts.MediaAssetStore.Get(r.Context(), media.AccessPolicy{TenantID: resolved.TenantID, UserID: resolved.UserID}, assetID)
		if err != nil {
			writeImageError(w, err)
			return
		}
		key := strings.TrimSpace(asset.Original.Path)
		if key == "" {
			writeTenantError(w, http.StatusNotFound, "image blob unavailable")
			return
		}
		reader, err := opts.ImageBlobStore.Open(r.Context(), media.AccessPolicy{TenantID: resolved.TenantID, UserID: resolved.UserID, SessionID: asset.SessionID}, key)
		if err != nil {
			writeImageError(w, err)
			return
		}
		defer reader.Close()
		contentType := asset.MediaType
		if contentType == "" {
			contentType = reader.Blob.MediaType
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", safeImageFilename(asset.Name)))
		if asset.SizeBytes > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
		}
		_, _ = io.Copy(w, reader)
	})
}

func resolveImageSession(w http.ResponseWriter, r *http.Request, opts Options) (context.Context, uint64, bool) {
	sessionID, ok := imageSessionIDFromPath(r.URL.Path)
	if !ok || sessionID == 0 {
		writeTenantError(w, http.StatusBadRequest, "session id is required")
		return r.Context(), 0, false
	}
	if _, err := opts.TenantService.GetSession(r.Context(), sessionID); err != nil {
		writeTenantServiceError(w, err)
		return r.Context(), 0, false
	}
	return r.Context(), sessionID, true
}

func imageSessionIDFromPath(raw string) (uint64, bool) {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 4 || parts[0] != "tenant" || parts[1] != "sessions" {
		return 0, false
	}
	id, err := strconv.ParseUint(parts[2], 10, 64)
	return id, err == nil
}

func safeImageFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "image"
	}
	name = path.Base(name)
	if mime.TypeByExtension(path.Ext(name)) == "" {
		return "image"
	}
	return strings.ReplaceAll(name, "\"", "")
}

func firstImageValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func writeImageError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, imagegen.ErrInvalidImageRequest):
		status = http.StatusBadRequest
	case errors.Is(err, imagegen.ErrImageGenerationDisabled):
		status = http.StatusConflict
	case errors.Is(err, imagegen.ErrBlobNotFound), errors.Is(err, media.ErrNotFound), errors.Is(err, mysqlstore.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, imagegen.ErrBlobForbidden), errors.Is(err, media.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, imagegen.ErrProviderTimeout), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	writeTenantError(w, status, err.Error())
}
