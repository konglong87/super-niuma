package server

import (
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func queryAttachmentsFromMobile(attachments []mobileAttachment) []QueryAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]QueryAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, QueryAttachment{
			AttachmentID: attachment.AttachmentID,
			Type:         attachment.Type,
			MediaType:    attachment.MediaType,
			Name:         attachment.Name,
			URL:          attachment.URL,
			SizeBytes:    attachment.SizeBytes,
			SHA256:       attachment.SHA256,
			Transcript:   attachment.Transcript,
		})
	}
	return out
}

func multimodalModelForAttachments(settings config.Settings, primaryModel string, attachments []QueryAttachment) string {
	model := strings.TrimSpace(primaryModel)
	for _, attachment := range attachments {
		modality := strings.TrimSpace(attachment.Type)
		if modality == "" {
			modality = attachment.MediaType
		}
		candidate := config.MultimodalModelFor(settings, model, modality)
		if strings.TrimSpace(candidate) != "" && candidate != model {
			return candidate
		}
	}
	return model
}

func mobileModelForRequest(opts Options, req mobileMessageStreamRequest, session mysqlstore.Session) string {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(session.Model)
	}
	attachments := queryAttachmentsFromMobile(req.Attachments)
	return multimodalModelForAttachments(config.LoadForCWD(opts.Workspace).Settings, model, attachments)
}
