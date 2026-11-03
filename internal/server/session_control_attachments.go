package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	_ "golang.org/x/image/webp"
)

const (
	sessionControlImagePrefix          = "sc-image-"
	sessionControlImageURLPrefix       = "/tenant/media/assets/"
	sessionControlImageMaxPixels       = 100_000_000
	sessionControlImageMaxRequestBytes = ((maxAgentTaskAttachmentBytes+2)/3*4)*maxAgentTaskAttachments + (1 << 20)
)

func isSessionControlMessageRequest(r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return r.Method == http.MethodPost && len(parts) == 6 && parts[0] == "tenant" && parts[1] == "session-control" && parts[2] == "sessions" && parts[3] == "tenant" && parts[4] != "" && parts[5] == "messages"
}

func prepareSessionControlAttachments(ctx context.Context, opts Options, scope sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, inputs []agentTaskAttachmentRequest) ([]agenttasks.Attachment, error) {
	if len(inputs) > maxAgentTaskAttachments {
		return nil, errors.New("too many attachments")
	}
	needsStorage := false
	for _, input := range inputs {
		needsStorage = needsStorage || strings.TrimSpace(input.InlineData) != "" || sessionControlImageID(input.AttachmentID, input.URL) != ""
	}
	policy := media.AccessPolicy{TenantID: scope.TenantID, UserID: scope.UserID}
	if needsStorage {
		if opts.ImageBlobStore == nil || opts.MediaAssetStore == nil {
			return nil, errors.New("image attachment storage is unavailable")
		}
		snapshot, err := opts.SessionControl.Get(ctx, sessioncontrol.GetRequest{Context: scope, Ref: ref})
		if err != nil {
			return nil, err
		}
		if ref.Source != sessioncontrol.SourceTenant || snapshot.Ref != ref || snapshot.ID == 0 {
			return nil, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "image attachment session is not authorized"}
		}
		policy.SessionID = snapshot.ID
	}
	attachments := make([]agenttasks.Attachment, 0, len(inputs))
	for _, input := range inputs {
		var attachment agenttasks.Attachment
		var err error
		switch {
		case strings.TrimSpace(input.InlineData) != "":
			attachment, err = archiveSessionControlImage(ctx, opts, policy, input)
		case sessionControlImageID(input.AttachmentID, input.URL) != "":
			var asset media.Asset
			asset, err = readSessionControlImageAsset(ctx, opts, policy, sessionControlImageID(input.AttachmentID, input.URL))
			if err == nil {
				attachment = sessionControlAssetAttachment(asset)
			}
		default:
			attachment, err = normalizeAgentTaskAttachment(input)
		}
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

func archiveSessionControlImage(ctx context.Context, opts Options, policy media.AccessPolicy, input agentTaskAttachmentRequest) (agenttasks.Attachment, error) {
	if strings.TrimSpace(input.URL) != "" {
		return agenttasks.Attachment{}, errors.New("inline image must not also provide a URL")
	}
	normalized, err := normalizeAgentTaskAttachment(input)
	if err != nil {
		return agenttasks.Attachment{}, err
	}
	data, err := base64.StdEncoding.DecodeString(normalized.InlineData)
	if err != nil || int64(len(data)) != normalized.SizeBytes {
		return agenttasks.Attachment{}, errors.New("inline image size does not match size_bytes")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > sessionControlImageMaxPixels {
		return agenttasks.Attachment{}, errors.New("inline image format or dimensions are invalid")
	}
	mediaType := "image/" + format
	if mediaType != normalized.MediaType {
		return agenttasks.Attachment{}, errors.New("inline image media_type does not match image content")
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	if normalized.SHA256 != "" && !strings.EqualFold(normalized.SHA256, digest) {
		return agenttasks.Attachment{}, errors.New("inline image sha256 does not match image content")
	}
	scopedHash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s", policy.TenantID, policy.UserID, policy.SessionID, digest)))
	assetID := sessionControlImagePrefix + hex.EncodeToString(scopedHash[:])
	key := fmt.Sprintf("tenant-%d/user-%d/session-%d/%s.%s", policy.TenantID, policy.UserID, policy.SessionID, assetID, format)
	name := safeImageFilename(normalized.Name)
	if normalized.Name == "" {
		name = assetID + "." + format
	}
	blob, err := opts.ImageBlobStore.Put(ctx, imagegen.PutBlobRequest{Policy: policy, Key: key, MediaType: mediaType, Name: name, Data: bytes.NewReader(data)})
	if err != nil {
		return agenttasks.Attachment{}, errors.New("image attachment could not be archived")
	}
	// Original owns the content digest. The optional asset-level hash is a
	// tenant-wide dedup key, which cannot represent exact Session isolation.
	asset := media.Asset{AssetID: assetID, Kind: media.KindImage, MediaType: mediaType, Name: name, SizeBytes: blob.SizeBytes, State: media.StateReady, TenantID: policy.TenantID, UserID: policy.UserID, SessionID: policy.SessionID, Access: policy, Original: media.Variant{Path: blob.Key, MediaType: mediaType, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256}}
	if err := opts.MediaAssetStore.Put(ctx, asset); err != nil {
		return agenttasks.Attachment{}, errors.New("image attachment metadata could not be archived")
	}
	return sessionControlAssetAttachment(asset), nil
}

func sessionControlAssetAttachment(asset media.Asset) agenttasks.Attachment {
	return agenttasks.Attachment{AttachmentID: asset.AssetID, Type: media.KindImage, MediaType: asset.MediaType, Name: asset.Name, URL: sessionControlImageURLPrefix + asset.AssetID, SizeBytes: asset.SizeBytes, SHA256: asset.Original.SHA256}
}

func sessionControlImageID(attachmentID, attachmentURL string) string {
	if strings.HasPrefix(attachmentID, sessionControlImagePrefix) {
		return attachmentID
	}
	if strings.HasPrefix(attachmentURL, sessionControlImageURLPrefix+sessionControlImagePrefix) {
		return strings.TrimPrefix(attachmentURL, sessionControlImageURLPrefix)
	}
	return ""
}

func readSessionControlImageAsset(ctx context.Context, opts Options, policy media.AccessPolicy, assetID string) (media.Asset, error) {
	if opts.MediaAssetStore == nil || opts.ImageBlobStore == nil {
		return media.Asset{}, errors.New("image attachment storage is unavailable")
	}
	asset, err := opts.MediaAssetStore.Get(ctx, policy, assetID)
	if err != nil || asset.TenantID != policy.TenantID || asset.UserID != policy.UserID || asset.SessionID != policy.SessionID || asset.State != media.StateReady || asset.Kind != media.KindImage {
		return media.Asset{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "image attachment is unavailable for this session"}
	}
	return asset, nil
}

// Durable references are hydrated for every execution, including queued and
// recovered Runs. Only these in-memory query values carry the image bytes.
func agentTaskQueryAttachments(ctx context.Context, opts Options, task mysqlstore.AgentTask, attachments []agenttasks.Attachment) ([]QueryAttachment, error) {
	result := queryAttachmentsFromAgentTask(attachments)
	policy := media.AccessPolicy{TenantID: task.TenantID, UserID: task.UserID, SessionID: task.ParentSessionID}
	for i := range result {
		id := sessionControlImageID(result[i].AttachmentID, result[i].URL)
		if id == "" {
			continue
		}
		asset, err := readSessionControlImageAsset(ctx, opts, policy, id)
		if err != nil {
			return nil, err
		}
		reader, err := opts.ImageBlobStore.Open(ctx, policy, asset.Original.Path)
		if err != nil {
			return nil, errors.New("image attachment blob is unavailable")
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maxAgentTaskAttachmentBytes+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(data)) != asset.SizeBytes || int64(len(data)) > maxAgentTaskAttachmentBytes {
			return nil, errors.New("image attachment blob is invalid")
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != asset.Original.SHA256 {
			return nil, errors.New("image attachment integrity check failed")
		}
		result[i].MediaType, result[i].SizeBytes, result[i].SHA256 = asset.MediaType, asset.SizeBytes, asset.Original.SHA256
		result[i].URL, result[i].InlineData = "", base64.StdEncoding.EncodeToString(data)
	}
	return result, nil
}

func cloneSessionControlAttachments(ctx context.Context, opts Options, source mysqlstore.AgentTask, targetSessionID uint64, attachments []agenttasks.Attachment) ([]agenttasks.Attachment, error) {
	result := append([]agenttasks.Attachment(nil), attachments...)
	for i, attachment := range attachments {
		if sessionControlImageID(attachment.AttachmentID, attachment.URL) == "" {
			continue
		}
		hydrated, err := agentTaskQueryAttachments(ctx, opts, source, []agenttasks.Attachment{attachment})
		if err != nil {
			return nil, err
		}
		input := hydrated[0]
		policy := media.AccessPolicy{TenantID: source.TenantID, UserID: source.UserID, SessionID: targetSessionID}
		result[i], err = archiveSessionControlImage(ctx, opts, policy, agentTaskAttachmentRequest{Type: input.Type, MediaType: input.MediaType, Name: input.Name, SizeBytes: input.SizeBytes, SHA256: input.SHA256, InlineData: input.InlineData})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
