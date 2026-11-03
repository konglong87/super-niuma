package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/quota"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

const (
	mobileMessagePending   = "pending"
	mobileMessageStreaming = "streaming"
	mobileMessageCompleted = "completed"
	mobileMessageFailed    = "failed"
	mobileMessageCancelled = "cancelled"
)

type mobileClaims struct {
	TenantKey          string   `json:"tenant_key"`
	TenantID           uint64   `json:"tenant_id,omitempty"`
	UserID             string   `json:"user_id"`
	DeviceID           string   `json:"device_id,omitempty"`
	ExpiresAt          int64    `json:"exp,omitempty"`
	Subject            string   `json:"sub,omitempty"`
	AllowedModels      []string `json:"allowed_models,omitempty"`
	RateLimitPerMinute int      `json:"rate_limit_per_minute,omitempty"`
	DailyMessageQuota  int      `json:"daily_message_quota,omitempty"`
	DailyTokenQuota    int      `json:"daily_token_quota,omitempty"`
}

type mobileJWTHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ,omitempty"`
}

type mobileSessionRequest struct {
	SessionKey   string `json:"session_key,omitempty"`
	Title        string `json:"title,omitempty"`
	Status       string `json:"status,omitempty"`
	Model        string `json:"model,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	MetadataJSON string `json:"metadata_json,omitempty"`
}

type mobileMessageStreamRequest struct {
	Content          string             `json:"content"`
	Model            string             `json:"model,omitempty"`
	MessageKey       string             `json:"message_key,omitempty"`
	Attachments      []mobileAttachment `json:"attachments,omitempty"`
	ProfileID        string             `json:"profile_id,omitempty"`
	ProfileVersion   uint               `json:"profile_version,omitempty"`
	ProfileOverrides map[string]any     `json:"profile_overrides,omitempty"`
}

type mobileMessageRegenerateRequest struct {
	Model      string `json:"model,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
}

type mobileBranchRequest struct {
	UntilTurn      uint   `json:"until_turn,omitempty"`
	UntilMessageID uint64 `json:"until_message_id,omitempty"`
	Title          string `json:"title,omitempty"`
	SessionKey     string `json:"session_key,omitempty"`
}

type mobileAttachmentPresignRequest struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Name      string `json:"name,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type mobileAttachment struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Type         string `json:"type"`
	MediaType    string `json:"media_type,omitempty"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Transcript   string `json:"transcript,omitempty"`
}

type mobileSSEEvent struct {
	Type       string `json:"type"`
	SessionID  uint64 `json:"session_id,omitempty"`
	MessageID  uint64 `json:"message_id,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
	Status     string `json:"status,omitempty"`
	Delta      string `json:"delta,omitempty"`
	Error      string `json:"error,omitempty"`
}

type mobileLatestRecap struct {
	MessageID uint64    `json:"message_id,omitempty"`
	Content   string    `json:"content"`
	Model     string    `json:"model,omitempty"`
	TurnIndex uint      `json:"turn_index,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type mobileSessionDetailResponse struct {
	mysqlstore.Session
	LatestRecap *mobileLatestRecap `json:"latest_recap,omitempty"`
}

type mobileMessageMetadata struct {
	MessageKey  string             `json:"message_key,omitempty"`
	Status      string             `json:"status,omitempty"`
	DeviceID    string             `json:"device_id,omitempty"`
	Error       string             `json:"error,omitempty"`
	Attachments []mobileAttachment `json:"attachments,omitempty"`
}

func registerMobileRoutes(router *gin.Engine, opts Options) {
	group := router.Group("/mobile/chat")
	group.Use(mobileJWTMiddleware(opts), mobileAccessLogMiddleware(), mobileBodySummaryLogMiddleware())
	group.GET("/ws", mobileWebSocketGin(opts))
	group.POST("/attachments/presign", mobilePresignAttachmentGin(opts))
	group.GET("/sessions", mobileListSessionsGin(opts))
	group.POST("/sessions", mobileCreateSessionGin(opts))
	group.GET("/sessions/:id", mobileGetSessionGin(opts))
	group.PATCH("/sessions/:id", mobileUpdateSessionGin(opts))
	group.PUT("/sessions/:id", mobileUpdateSessionGin(opts))
	group.DELETE("/sessions/:id", mobileArchiveSessionGin(opts))
	group.POST("/sessions/:id/branch", mobileBranchSessionGin(opts))
	group.GET("/sessions/:id/messages", mobileListMessagesGin(opts))
	group.POST("/sessions/:id/messages/stream", mobileMessageStreamGin(opts))
	group.POST("/sessions/:id/messages/:message_id/cancel", mobileCancelMessageGin(opts))
	group.POST("/sessions/:id/messages/:message_id/regenerate", mobileRegenerateMessageGin(opts))
}

func mobilePresignAttachmentGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := mobileClaimsFromGin(c)
		policy := mobilePolicyFromGin(c)
		var req mobileAttachmentPresignRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		attachmentType := strings.TrimSpace(req.Type)
		if err := mobileValidateAttachmentUpload(req, policy); err != nil {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		attachmentID := mobileGeneratedKey("att")
		objectKey := mobileAttachmentObjectKey(claims, attachmentID, req.Name)
		uploadURL := mobileAttachmentURL(opts.MobileUploadBaseURL, objectKey)
		attachmentURL := uploadURL
		expiresAt := time.Now().Add(15 * time.Minute).UTC()
		headers := map[string]string{
			"content-type": req.MediaType,
			"x-sha256":     req.SHA256,
		}
		if opts.MobileUploadSigner != nil {
			// A configured signer switches the contract from local placeholder URLs
			// to real object-store PUT URLs while keeping the response shape stable.
			signed, err := opts.MobileUploadSigner.PresignUpload(c.Request.Context(), MobileUploadSignRequest{
				ObjectKey: objectKey,
				MediaType: req.MediaType,
				SizeBytes: req.SizeBytes,
				SHA256:    req.SHA256,
				Expires:   15 * time.Minute,
			})
			if err != nil {
				mobileGinError(c, http.StatusServiceUnavailable, err.Error())
				return
			}
			if strings.TrimSpace(signed.ObjectKey) != "" {
				objectKey = signed.ObjectKey
			}
			uploadURL = signed.UploadURL
			attachmentURL = signed.PublicURL
			if attachmentURL == "" {
				attachmentURL = mobileAttachmentURL(opts.MobileUploadBaseURL, objectKey)
			}
			if !signed.ExpiresAt.IsZero() {
				expiresAt = signed.ExpiresAt
			}
			if len(signed.Headers) > 0 {
				headers = signed.Headers
			}
		}
		if opts.MediaAssetStore != nil {
			userID, parseErr := strconv.ParseUint(strings.TrimSpace(claims.UserID), 10, 64)
			if parseErr == nil && claims.TenantID != 0 {
				asset := media.Asset{AssetID: attachmentID, Kind: attachmentType, MediaType: req.MediaType, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256, State: media.StateUploading, TenantID: claims.TenantID, UserID: userID, Original: media.Variant{URL: attachmentURL, MediaType: req.MediaType, SizeBytes: req.SizeBytes, SHA256: req.SHA256}, Access: media.AccessPolicy{TenantID: claims.TenantID, UserID: userID}, ExpiresAt: expiresAt}
				if err := opts.MediaAssetStore.Put(c.Request.Context(), asset); err != nil {
					mobileGinError(c, http.StatusServiceUnavailable, "persist media asset: "+err.Error())
					return
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"attachment_id":  attachmentID,
			"object_key":     objectKey,
			"upload_url":     uploadURL,
			"expires_at":     expiresAt.Format(time.RFC3339),
			"max_size_bytes": policy.MaxAttachmentBytes,
			"headers":        headers,
			"attachment": mobileAttachment{
				AttachmentID: attachmentID,
				Type:         attachmentType,
				MediaType:    req.MediaType,
				Name:         req.Name,
				URL:          attachmentURL,
				SizeBytes:    req.SizeBytes,
				SHA256:       req.SHA256,
			},
		})
	}
}

func mobileListSessionsGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !mobileRequireTenantService(c, opts) {
			return
		}
		items, err := opts.TenantService.ListSessions(c.Request.Context(), parseLimit(c.Query("limit")))
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": items})
	}
}

func mobileCreateSessionGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := mobileClaimsFromGin(c)
		if !mobileRequireTenantService(c, opts) {
			return
		}
		var req mobileSessionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := mobileValidateModel(req.Model, mobilePolicyFromGin(c)); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		sessionKey := strings.TrimSpace(req.SessionKey)
		if sessionKey == "" {
			sessionKey = mobileGeneratedKey("sess")
		}
		metadata := req.MetadataJSON
		if metadata == "" && claims.DeviceID != "" {
			metadata = mobileMetadataJSON(claims, "", mobileMessageCompleted)
		}
		id, err := opts.TenantService.UpsertSession(c.Request.Context(), tenantservice.SessionRequest{
			SessionKey:   sessionKey,
			Title:        req.Title,
			Status:       req.Status,
			Model:        req.Model,
			CWD:          req.CWD,
			MetadataJSON: metadata,
		})
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "session_key": sessionKey})
	}
}

func mobileGetSessionGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !mobileRequireTenantService(c, opts) {
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		item, err := opts.TenantService.GetSession(c.Request.Context(), sessionID)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		resp := mobileSessionDetailResponse{Session: item}
		// 求「最新 recap」必须倒着读。原先是 ListMessages(…, 1000)，升序 + 被
		// normalizeLimit 夹到 500，于是求出来的是「最旧 500 条里的最新」——
		// 长会话里新生成的 recap 根本不出现（TODO-119）。
		messages, err := opts.TenantService.ListRecentMessages(c.Request.Context(), sessionID, mobileRecapScanMessages)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		resp.LatestRecap = mobileLatestRecapFromMessages(messages)
		c.JSON(http.StatusOK, resp)
	}
}

func mobileUpdateSessionGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !mobileRequireTenantService(c, opts) {
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		var req mobileSessionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := mobileValidateModel(req.Model, mobilePolicyFromGin(c)); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		err := opts.TenantService.UpdateSession(c.Request.Context(), sessionID, tenantservice.SessionRequest{
			Title:        req.Title,
			Status:       req.Status,
			Model:        req.Model,
			CWD:          req.CWD,
			MetadataJSON: req.MetadataJSON,
		})
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": sessionID})
	}
}

func mobileArchiveSessionGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !mobileRequireTenantService(c, opts) {
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		if err := opts.TenantService.ArchiveSession(c.Request.Context(), sessionID); err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": sessionID, "archived": true})
	}
}

func mobileBranchSessionGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !mobileRequireTenantService(c, opts) {
			return
		}
		claims := mobileClaimsFromGin(c)
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		var req mobileBranchRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		source, err := opts.TenantService.GetSession(c.Request.Context(), sessionID)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		// fork 需要的是「整个会话」而不是「一页会话」，所以必须走 ListAllMessages：
		// ListMessages 的 limit 会被 normalizeLimit 静默夹到 500，原先传 1000 当"全部"
		// 用，于是超过 500 条的会话被复制成一个悄悄缺了尾巴的分支，还照样返回 200
		// 和 copied_messages: 500，连 until_turn 也是错的（TODO-115）。
		// 超限现在报 ErrTooManyMessages → 422，而不是静默少复制。
		messages, err := opts.TenantService.ListAllMessages(c.Request.Context(), sessionID, mysqlstore.MaxForkedMessages)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		untilTurn := req.UntilTurn
		if req.UntilMessageID > 0 {
			message, ok := mobileFindMessageByID(messages, req.UntilMessageID)
			if !ok {
				mobileGinError(c, http.StatusNotFound, "message not found")
				return
			}
			untilTurn = message.TurnIndex
		}
		if untilTurn == 0 {
			for _, message := range messages {
				if message.TurnIndex > untilTurn {
					untilTurn = message.TurnIndex
				}
			}
		}
		sessionKey := strings.TrimSpace(req.SessionKey)
		if sessionKey == "" {
			sessionKey = mobileGeneratedKey("branch")
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = strings.TrimSpace(source.Title)
			if title == "" {
				title = "Branch"
			} else {
				title += " branch"
			}
		}
		metadata, _ := json.Marshal(map[string]any{
			"mobile": map[string]any{
				"parent_session_id": sessionID,
				"until_turn":        untilTurn,
				"device_id":         claims.DeviceID,
			},
		})
		// 分支会话行和它的全部消息交给 ForkSession 一次写完：这里曾经先建会话
		// 再逐条复制，中途失败会留下一个只复制了一半消息的分支会话（TODO-106）。
		var copies []tenantservice.MessageRequest
		for _, message := range messages {
			if message.TurnIndex > untilTurn {
				continue
			}
			copies = append(copies, tenantservice.MessageRequest{
				TurnIndex:   message.TurnIndex,
				Role:        message.Role,
				Content:     message.Content,
				ContentJSON: message.ContentJSON,
				ToolID:      message.ToolID,
				ToolName:    message.ToolName,
				IsError:     message.IsError,
				Model:       message.Model,
				InputTokens: message.InputTokens,
				OutputToken: message.OutputTokens,
				TraceID:     observability.TraceID(c.Request.Context()),
			})
		}
		forked, err := opts.TenantService.ForkSession(c.Request.Context(), tenantservice.ForkSessionRequest{
			Session: tenantservice.SessionRequest{
				SessionKey:   sessionKey,
				Title:        title,
				Status:       source.Status,
				Model:        source.Model,
				CWD:          source.CWD,
				MetadataJSON: string(metadata),
			},
			Messages: copies,
		})
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": forked.SessionID, "session_key": sessionKey, "copied_messages": forked.CopiedMessages, "parent_session_id": sessionID, "until_turn": untilTurn})
	}
}

func mobileListMessagesGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !mobileRequireTenantService(c, opts) {
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		if _, err := opts.TenantService.GetSession(c.Request.Context(), sessionID); err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		limit := parseLimit(c.Query("limit"))
		afterTurn, validAfterTurn := parseOptionalUint(c.Query("after_turn"))
		if !validAfterTurn {
			mobileGinError(c, http.StatusBadRequest, "after_turn must be an unsigned integer")
			return
		}
		cursorTurn, validCursor := mobileDecodeCursor(c.Query("cursor"))
		if !validCursor {
			mobileGinError(c, http.StatusBadRequest, "cursor must be an unsigned integer")
			return
		}
		if cursorTurn > 0 {
			afterTurn = cursorTurn
		}
		queryLimit := limit + 1
		if afterTurn > 0 || queryLimit <= 1 {
			queryLimit = 1000
		}
		items, err := opts.TenantService.ListMessages(c.Request.Context(), sessionID, queryLimit)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		items, nextCursor, hasMore := mobileFilterMessagesPage(items, uint(afterTurn), limit)
		c.JSON(http.StatusOK, gin.H{"data": items, "next_cursor": nextCursor, "has_more": hasMore})
	}
}

func mobileMessageStreamGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestStart := time.Now()
		claims := mobileClaimsFromGin(c)
		policy := mobilePolicyFromGin(c)
		if !mobileRequireTenantService(c, opts) {
			return
		}
		if opts.StreamQueryFunc == nil {
			mobileGinError(c, http.StatusServiceUnavailable, "chat stream is not configured")
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		var req mobileMessageStreamRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		req.Content = strings.TrimSpace(req.Content)
		if err := mobileValidateModel(req.Model, policy); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		if err := mobileValidateAttachments(req.Attachments, policy); err != nil {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		if req.Content == "" && len(req.Attachments) == 0 {
			mobileGinError(c, http.StatusBadRequest, "content or attachments are required")
			return
		}
		mobileEmitPhase(c.Request.Context(), "request.bind", requestStart, telemetry.StatusOK, "", req.Model, sessionID, map[string]any{
			"content_chars":     len([]rune(req.Content)),
			"attachment_count":  len(req.Attachments),
			"approx_input_size": mobileApproxTokens(req.Content) + mobileApproxAttachmentTokens(req.Attachments),
		})
		phaseStart := time.Now()
		session, err := opts.TenantService.GetSession(c.Request.Context(), sessionID)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		mobileEmitPhase(c.Request.Context(), "session.load", phaseStart, telemetry.StatusOK, "", req.Model, sessionID, map[string]any{"session_key": session.SessionKey})
		effectiveModel := mobileModelForRequest(opts, req, session)
		if req.Model == "" && effectiveModel != "" {
			if err := mobileValidateModel(effectiveModel, policy); err != nil {
				mobileGinPolicyError(c, err)
				return
			}
		}
		messageKey := strings.TrimSpace(req.MessageKey)
		var existingUser mysqlstore.Message
		hasExistingUser := false
		if messageKey == "" {
			messageKey = mobileGeneratedKey("msg")
		} else {
			// 幂等重放走两次定点查询。原先是 ListMessages(…, 1000) 再在返回切片里线性
			// 查找 message_key，而 normalizeLimit 把 limit 静默夹到 500 —— 长会话里客户端
			// 重试**找不到**已有记录，于是重新跑一次查询、重新写一条消息、重新计一次量。
			// 这一族夹取 bug 里只有这一处花钱且有副作用（TODO-118）。
			//
			// 顺序有意：先找 assistant，命中就是「上一次已经答完」，直接回放，
			// **不消耗任何配额**（下面的 Reserve / reserveQueryQuota 都在这之后）。
			// 没有 assistant 才找 user —— 那是「上一次写下了请求但流断了」，
			// 这一次要复用那条 user 消息的轮次，不能再写一条。
			phaseStart = time.Now()
			existingAssistant, replay, err := opts.TenantService.MessageByKey(c.Request.Context(), sessionID, "assistant", messageKey)
			if err != nil {
				mobileGinTenantServiceError(c, err)
				return
			}
			if !replay {
				existingUser, hasExistingUser, err = opts.TenantService.MessageByKey(c.Request.Context(), sessionID, "user", messageKey)
				if err != nil {
					mobileGinTenantServiceError(c, err)
					return
				}
			}
			mobileEmitPhase(c.Request.Context(), "message.dedupe", phaseStart, telemetry.StatusOK, "", effectiveModel, sessionID, map[string]any{
				"message_key":  messageKey,
				"replayed":     replay,
				"resumed_user": hasExistingUser,
			})
			if replay {
				mobileReplayMessageSSE(c.Writer, sessionID, existingAssistant, messageKey)
				return
			}
		}
		estimatedTokens := mobileApproxTokens(req.Content) + mobileApproxAttachmentTokens(req.Attachments)
		if err := opts.MobileUsageStore.Reserve(mobileUsageKey(claims), policy, estimatedTokens); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		reservation, reserved := reserveQueryQuota(c.Request.Context(), opts, quota.SourceMobile, "/mobile/chat/sessions/:id/messages/stream", QueryRequest{
			Prompt:      req.Content,
			Model:       effectiveModel,
			MaxTokens:   0,
			Attachments: queryAttachmentsFromMobile(req.Attachments),
		})
		if reserved.err != nil {
			releaseMobileUsageReservation(opts.MobileUsageStore, mobileUsageKey(claims), estimatedTokens)
			mobileGinPolicyError(c, reserved.err)
			return
		}
		userTurn, assistantTurn := uint(0), uint(0)
		if hasExistingUser {
			// 复用上一次那条 user 消息的轮次，但 assistant 的轮次必须**问数据库**。
			// 原先它取自 mobileMaxTurn(existingMessages)，而那就是被夹到 500 的列表 ——
			// 算出来的「下一轮」是 501，而消息表的唯一键是 (session_id, turn_index) 且
			// 写入走 ON DUPLICATE KEY UPDATE，于是新 assistant 直接覆盖掉第 501 条已有
			// 消息（TODO-116 的同一族后果，落在这个分支上）。
			userTurn = existingUser.TurnIndex
			phaseStart = time.Now()
			maxTurn, err := opts.TenantService.MaxMessageTurn(c.Request.Context(), sessionID)
			if err != nil {
				mobileGinTenantServiceError(c, err)
				return
			}
			assistantTurn = maxTurn + 1
			mobileEmitPhase(c.Request.Context(), "turn.allocate", phaseStart, telemetry.StatusOK, "", effectiveModel, sessionID, map[string]any{
				"user_turn":      userTurn,
				"assistant_turn": assistantTurn,
			})
		} else {
			var err error
			phaseStart = time.Now()
			userTurn, assistantTurn, err = mobileNextTurns(c.Request.Context(), opts.TenantService, sessionID)
			if err != nil {
				settleReservedMobileQuota(c.Request.Context(), opts, reservation, claims, estimatedTokens, err)
				mobileGinTenantServiceError(c, err)
				return
			}
			mobileEmitPhase(c.Request.Context(), "turn.allocate", phaseStart, telemetry.StatusOK, "", effectiveModel, sessionID, map[string]any{
				"user_turn":      userTurn,
				"assistant_turn": assistantTurn,
			})
			phaseStart = time.Now()
			userMessageID, err := opts.TenantService.UpsertMessage(c.Request.Context(), tenantservice.MessageRequest{
				SessionID:   sessionID,
				TurnIndex:   userTurn,
				Role:        "user",
				Content:     req.Content,
				Model:       effectiveModel,
				ContentJSON: mobileMetadataJSONWithAttachments(claims, messageKey, mobileMessageCompleted, "", req.Attachments),
				TraceID:     observability.TraceID(c.Request.Context()),
			})
			if err != nil {
				settleReservedMobileQuota(c.Request.Context(), opts, reservation, claims, estimatedTokens, err)
				mobileGinTenantServiceError(c, err)
				return
			}
			maybeWriteExplicitRememberMemories(c.Request.Context(), opts.TenantService, req.Content, sessionID, userMessageID)
			maybeWriteAutoMemories(c.Request.Context(), opts.TenantService, req.Content, sessionID, userMessageID)
			mobileEmitPhase(c.Request.Context(), "user_message.persist", phaseStart, telemetry.StatusOK, "", effectiveModel, sessionID, map[string]any{
				"message_key": messageKey,
				"turn":        userTurn,
			})
		}
		phaseStart = time.Now()
		assistantID, err := opts.TenantService.UpsertMessage(c.Request.Context(), tenantservice.MessageRequest{
			SessionID:   sessionID,
			TurnIndex:   assistantTurn,
			Role:        "assistant",
			Model:       effectiveModel,
			ContentJSON: mobileMetadataJSON(claims, messageKey, mobileMessageStreaming),
			TraceID:     observability.TraceID(c.Request.Context()),
		})
		if err != nil {
			settleReservedMobileQuota(c.Request.Context(), opts, reservation, claims, estimatedTokens, err)
			mobileGinTenantServiceError(c, err)
			return
		}
		mobileEmitPhase(c.Request.Context(), "assistant_placeholder.persist", phaseStart, telemetry.StatusOK, "", effectiveModel, sessionID, map[string]any{
			"message_id":  assistantID,
			"message_key": messageKey,
			"turn":        assistantTurn,
		})
		streamCtx, cancel := context.WithCancel(c.Request.Context())
		opts.MobileStreams.RegisterFor(claims, sessionID, assistantID, messageKey, cancel)
		defer opts.MobileStreams.Unregister(sessionID, assistantID, messageKey)
		defer cancel()
		req.Model = effectiveModel
		mobileStreamChat(streamCtx, c.Writer, opts, req, session, assistantID, assistantTurn, messageKey, claims, reservation)
	}
}

func mobileCancelMessageGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := mobileClaimsFromGin(c)
		if !mobileRequireTenantService(c, opts) {
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		messageID, ok := mobileMessageIDParam(c)
		if !ok {
			return
		}
		if _, err := opts.TenantService.GetSession(c.Request.Context(), sessionID); err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		// 取消要的是「按 id 取这一条」。原先是 ListMessages(…, 1000) 再线性查找，而
		// normalizeLimit 把 limit 静默夹到 500，于是在超过 500 条的会话里取消一条
		// 真实存在的消息会得到 404 message not found（TODO-117）。
		message, err := opts.TenantService.GetMessage(c.Request.Context(), sessionID, messageID)
		if err != nil {
			mobileGinMessageLookupError(c, err, http.StatusNotFound, "message not found")
			return
		}
		metadata := mobileMessageMetadataFromJSON(message.ContentJSON)
		active := opts.MobileStreams.CancelFor(claims, sessionID, messageID, metadata.MessageKey)
		if _, err := opts.TenantService.UpsertMessage(c.Request.Context(), tenantservice.MessageRequest{
			SessionID:   sessionID,
			TurnIndex:   message.TurnIndex,
			Role:        message.Role,
			Content:     message.Content,
			ContentJSON: mobileMetadataJSONWithAttachments(claims, metadata.MessageKey, mobileMessageCancelled, "", metadata.Attachments),
			ToolID:      message.ToolID,
			ToolName:    message.ToolName,
			IsError:     true,
			Model:       message.Model,
			InputTokens: message.InputTokens,
			OutputToken: message.OutputTokens,
			TraceID:     observability.TraceID(c.Request.Context()),
		}); err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		opts.MobileStreams.BroadcastFor(claims, mobileSSEEvent{Type: "error", SessionID: sessionID, MessageID: messageID, MessageKey: metadata.MessageKey, Status: mobileMessageCancelled, Error: "stream cancelled"})
		c.JSON(http.StatusOK, gin.H{"id": messageID, "status": mobileMessageCancelled, "active": active})
	}
}

func mobileRegenerateMessageGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := mobileClaimsFromGin(c)
		policy := mobilePolicyFromGin(c)
		if !mobileRequireTenantService(c, opts) {
			return
		}
		if opts.StreamQueryFunc == nil {
			mobileGinError(c, http.StatusServiceUnavailable, "chat stream is not configured")
			return
		}
		sessionID, ok := mobileSessionIDParam(c)
		if !ok {
			return
		}
		messageID, ok := mobileMessageIDParam(c)
		if !ok {
			return
		}
		var req mobileMessageRegenerateRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			mobileGinError(c, http.StatusBadRequest, err.Error())
			return
		}
		session, err := opts.TenantService.GetSession(c.Request.Context(), sessionID)
		if err != nil {
			mobileGinTenantServiceError(c, err)
			return
		}
		// 重新生成要的是两条真实存在的消息：目标 assistant 和它前面那条 user。原先两
		// 步都在 ListMessages(…, 1000) 的返回切片里线性查找，而 normalizeLimit 把
		// limit 静默夹到 500，所以两条都在夹取之外，报 404 message not found。
		//
		// 两处都得是定点查询才算修完。只修目标那一步会更糟：夹取后的列表里仍然有
		// user 消息（第 499 条），在列表里找「target 之前最后一条 user」会挑中它并当成
		// 第 600 条的提示 —— 不再报错，而是**静默拿错的提示重新生成**。变异检查复现过
		// 这一幕（只回退这一步时 Prompt 变成 msg-499 而不是 msg-599）。只有当夹取窗口
		// 里连一条 user 消息都没有时才会退化成 400（TODO-117）。
		//
		// 原先那个在切片里线性查找的 mobileFindPreviousUserMessage 已随之删除 ——
		// 它只有这一个调用点，留着就是一个还在鼓励「先拉一页再筛」的死函数。
		target, err := opts.TenantService.GetMessage(c.Request.Context(), sessionID, messageID)
		if err != nil {
			mobileGinMessageLookupError(c, err, http.StatusNotFound, "message not found")
			return
		}
		if target.Role != "assistant" {
			mobileGinError(c, http.StatusBadRequest, "only assistant messages can be regenerated")
			return
		}
		userMessage, err := opts.TenantService.PreviousUserMessage(c.Request.Context(), sessionID, target.TurnIndex)
		if err != nil {
			mobileGinMessageLookupError(c, err, http.StatusBadRequest, "previous user message not found")
			return
		}
		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = target.Model
		}
		if err := mobileValidateModel(model, policy); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		metadata := mobileMessageMetadataFromJSON(userMessage.ContentJSON)
		messageKey := strings.TrimSpace(req.MessageKey)
		if messageKey == "" {
			messageKey = mobileGeneratedKey("regen")
		} else {
			// 与 stream 同一处夹取、同一个后果：原先这里扫 ListMessages(…, 1000) 的返回
			// 切片找 message_key，长会话里重试漏判，于是重新跑一次查询、重新写一条消息、
			// 重新计一次量。regenerate 只需要 assistant 那一条 —— 它不写 user 消息，
			// 提示是从目标前面那条 user 消息读出来的（TODO-118）。
			existing, replay, err := opts.TenantService.MessageByKey(c.Request.Context(), sessionID, "assistant", messageKey)
			if err != nil {
				mobileGinTenantServiceError(c, err)
				return
			}
			if replay {
				mobileReplayMessageSSE(c.Writer, sessionID, existing, messageKey)
				return
			}
		}
		streamReq := mobileMessageStreamRequest{
			Content:     strings.TrimSpace(userMessage.Content),
			Model:       model,
			MessageKey:  messageKey,
			Attachments: metadata.Attachments,
		}
		model = mobileModelForRequest(opts, streamReq, session)
		streamReq.Model = model
		if err := mobileValidateModel(model, policy); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		estimatedTokens := mobileApproxTokens(streamReq.Content) + mobileApproxAttachmentTokens(streamReq.Attachments)
		if err := opts.MobileUsageStore.Reserve(mobileUsageKey(claims), policy, estimatedTokens); err != nil {
			mobileGinPolicyError(c, err)
			return
		}
		reservation, reserved := reserveQueryQuota(c.Request.Context(), opts, quota.SourceMobile, "/mobile/chat/sessions/:id/messages/:message_id/regenerate", QueryRequest{
			Prompt:      streamReq.Content,
			Model:       model,
			Attachments: queryAttachmentsFromMobile(streamReq.Attachments),
		})
		if reserved.err != nil {
			releaseMobileUsageReservation(opts.MobileUsageStore, mobileUsageKey(claims), estimatedTokens)
			mobileGinPolicyError(c, reserved.err)
			return
		}
		assistantTurn, _, err := mobileNextTurns(c.Request.Context(), opts.TenantService, sessionID)
		if err != nil {
			settleReservedMobileQuota(c.Request.Context(), opts, reservation, claims, estimatedTokens, err)
			mobileGinTenantServiceError(c, err)
			return
		}
		assistantID, err := opts.TenantService.UpsertMessage(c.Request.Context(), tenantservice.MessageRequest{
			SessionID:   sessionID,
			TurnIndex:   assistantTurn,
			Role:        "assistant",
			Model:       model,
			ContentJSON: mobileMetadataJSON(claims, messageKey, mobileMessageStreaming),
			TraceID:     observability.TraceID(c.Request.Context()),
		})
		if err != nil {
			settleReservedMobileQuota(c.Request.Context(), opts, reservation, claims, estimatedTokens, err)
			mobileGinTenantServiceError(c, err)
			return
		}
		streamCtx, cancel := context.WithCancel(c.Request.Context())
		opts.MobileStreams.RegisterFor(claims, sessionID, assistantID, messageKey, cancel)
		defer opts.MobileStreams.Unregister(sessionID, assistantID, messageKey)
		defer cancel()
		mobileStreamChat(streamCtx, c.Writer, opts, streamReq, session, assistantID, assistantTurn, messageKey, claims, reservation)
	}
}

func mobileStreamChat(ctx context.Context, w http.ResponseWriter, opts Options, req mobileMessageStreamRequest, session mysqlstore.Session, assistantID uint64, assistantTurn uint, messageKey string, claims mobileClaims, reservation quota.Reservation) {
	settler := newQuotaSettler(ctx, opts, reservation)
	defer settler.settleOnPanic()
	start := time.Now()
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	sessionID := session.ID
	telemetry.Emit(ctx, telemetry.Event{
		Name:      "mobile.chat.stream.started",
		Category:  telemetry.CategoryMobile,
		Source:    "server.mobileStreamChat",
		Status:    telemetry.StatusStarted,
		Model:     req.Model,
		SessionID: sessionID,
		Properties: map[string]any{
			"message_id":  assistantID,
			"message_key": messageKey,
			"device_id":   claims.DeviceID,
		},
	})
	phaseStart := time.Now()
	writeMobileRealtimeEvent(w, opts, claims, mobileSSEEvent{Type: "message_start", SessionID: sessionID, MessageID: assistantID, MessageKey: messageKey, Status: mobileMessageStreaming})
	mobileEmitPhase(ctx, "sse.message_start", phaseStart, telemetry.StatusOK, "", req.Model, sessionID, map[string]any{
		"message_id":  assistantID,
		"message_key": messageKey,
	})
	var streamed strings.Builder
	writer := mobileStreamWriter{w: w, opts: opts, claims: claims, sessionID: sessionID, messageID: assistantID, messageKey: messageKey, text: &streamed}
	prompt := mobilePromptWithAttachments(req.Content, req.Attachments)
	phaseStart = time.Now()
	profileSurface := ""
	if strings.TrimSpace(req.ProfileID) != "" || req.ProfileVersion > 0 || len(req.ProfileOverrides) > 0 {
		profileSurface = "mobile_chat"
	}
	result, err := opts.StreamQueryFunc(ctx, QueryRequest{Prompt: prompt, Model: req.Model, SessionKey: strconv.FormatUint(sessionID, 10), PromptMode: "chat", ProfileID: req.ProfileID, ProfileVersion: req.ProfileVersion, ProfileOverrides: req.ProfileOverrides, ProfileSurface: profileSurface, TenantSessionID: sessionID, Attachments: queryAttachmentsFromMobile(req.Attachments)}, writer)
	queryDuration := time.Since(phaseStart).Milliseconds()
	if err != nil {
		settler.settle(result, err)
		status := mobileMessageFailed
		messageError := err.Error()
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = mobileMessageCancelled
			messageError = "stream cancelled"
		}
		mobileEmitPhase(ctx, "query.run", phaseStart, telemetry.StatusError, messageError, req.Model, sessionID, map[string]any{
			"message_id":  assistantID,
			"message_key": messageKey,
			"duration_ms": queryDuration,
		})
		persistCtx := mobilePersistContext(ctx, claims)
		phaseStart = time.Now()
		_, _ = opts.TenantService.UpsertMessage(persistCtx, tenantservice.MessageRequest{
			SessionID:   sessionID,
			TurnIndex:   assistantTurn,
			Role:        "assistant",
			Content:     streamed.String(),
			Model:       req.Model,
			IsError:     true,
			ContentJSON: mobileMetadataJSONWithError(claims, messageKey, status, messageError),
			TraceID:     observability.TraceID(persistCtx),
		})
		mobileEmitPhase(ctx, "assistant_message.persist", phaseStart, telemetry.StatusError, messageError, req.Model, sessionID, map[string]any{
			"message_id":  assistantID,
			"message_key": messageKey,
			"status":      status,
		})
		phaseStart = time.Now()
		writeMobileRealtimeEvent(w, opts, claims, mobileSSEEvent{Type: "error", SessionID: sessionID, MessageID: assistantID, MessageKey: messageKey, Status: status, Error: messageError})
		mobileEmitPhase(ctx, "sse.error", phaseStart, telemetry.StatusError, messageError, req.Model, sessionID, map[string]any{
			"message_id":  assistantID,
			"message_key": messageKey,
			"status":      status,
		})
		telemetry.Emit(ctx, telemetry.Event{
			Name:       "mobile.chat.stream.finished",
			Category:   telemetry.CategoryMobile,
			Source:     "server.mobileStreamChat",
			Status:     telemetry.StatusError,
			Model:      req.Model,
			SessionID:  sessionID,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      messageError,
			Properties: map[string]any{
				"message_id":  assistantID,
				"message_key": messageKey,
				"status":      status,
			},
		})
		return
	}
	mobileEmitPhase(ctx, "query.run", phaseStart, telemetry.StatusOK, "", result.Model, sessionID, map[string]any{
		"message_id":  assistantID,
		"message_key": messageKey,
		"duration_ms": queryDuration,
	})
	content := result.Response
	if content == "" {
		content = streamed.String()
	}
	model := result.Model
	if model == "" {
		model = req.Model
	}
	settler.settle(result, nil)
	opts.MobileUsageStore.AddTokens(mobileUsageKey(claims), result.Usage.OutputTokens)
	phaseStart = time.Now()
	_, _ = opts.TenantService.UpsertMessage(ctx, tenantservice.MessageRequest{
		SessionID:   sessionID,
		TurnIndex:   assistantTurn,
		Role:        "assistant",
		Content:     content,
		Model:       model,
		InputTokens: uint(result.Usage.InputTokens),
		OutputToken: uint(result.Usage.OutputTokens),
		ContentJSON: mobileMetadataJSON(claims, messageKey, mobileMessageCompleted),
		TraceID:     observability.TraceID(ctx),
	})
	mobileEmitPhase(ctx, "assistant_message.persist", phaseStart, telemetry.StatusOK, "", model, sessionID, map[string]any{
		"message_id":     assistantID,
		"message_key":    messageKey,
		"input_tokens":   result.Usage.InputTokens,
		"output_tokens":  result.Usage.OutputTokens,
		"response_chars": len([]rune(content)),
		"streamed_chars": len([]rune(streamed.String())),
	})
	phaseStart = time.Now()
	writeMobileRealtimeEvent(w, opts, claims, mobileSSEEvent{Type: "message_stop", SessionID: sessionID, MessageID: assistantID, MessageKey: messageKey, Status: mobileMessageCompleted})
	mobileEmitPhase(ctx, "sse.message_stop", phaseStart, telemetry.StatusOK, "", model, sessionID, map[string]any{
		"message_id":  assistantID,
		"message_key": messageKey,
		"status":      mobileMessageCompleted,
	})
	telemetry.Emit(ctx, telemetry.Event{
		Name:                                "mobile.chat.stream.finished",
		Category:                            telemetry.CategoryMobile,
		Source:                              "server.mobileStreamChat",
		Status:                              telemetry.StatusOK,
		Model:                               model,
		SessionID:                           sessionID,
		DurationMS:                          time.Since(start).Milliseconds(),
		InputTokens:                         result.Usage.InputTokens,
		OutputTokens:                        result.Usage.OutputTokens,
		CacheCreationInputTokens:            result.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:                result.Usage.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: result.Usage.CacheCreationEphemeral1hInputTokens,
		CacheCreationEphemeral5mInputTokens: result.Usage.CacheCreationEphemeral5mInputTokens,
		Properties: map[string]any{
			"message_id":     assistantID,
			"message_key":    messageKey,
			"status":         mobileMessageCompleted,
			"response_chars": len([]rune(content)),
		},
	})
	mobileMaybeGenerateTitleAsync(ctx, opts, session, req.Content, content)
}

func releaseMobileUsageReservation(store MobileUsageStore, userKey string, estimatedTokens int) {
	if releaser, ok := store.(mobileUsageReleaser); ok {
		releaser.Release(userKey, estimatedTokens)
	}
}

func settleReservedMobileQuota(ctx context.Context, opts Options, reservation quota.Reservation, claims mobileClaims, estimatedTokens int, err error) {
	releaseMobileUsageReservation(opts.MobileUsageStore, mobileUsageKey(claims), estimatedTokens)
	settleQueryQuota(ctx, opts, reservation, query.Result{}, err)
}

func mobileEmitPhase(ctx context.Context, phase string, start time.Time, status string, errorMessage string, model string, sessionID uint64, props map[string]any) {
	if start.IsZero() {
		start = time.Now()
	}
	if props == nil {
		props = map[string]any{}
	}
	props["phase"] = phase
	telemetry.Emit(ctx, telemetry.Event{
		Name:       "mobile.phase." + phase + ".finished",
		Category:   telemetry.CategoryMobile,
		Source:     "server.mobile",
		Status:     status,
		Model:      model,
		SessionID:  sessionID,
		DurationMS: time.Since(start).Milliseconds(),
		Error:      errorMessage,
		Properties: props,
	})
}

type mobileStreamWriter struct {
	w          http.ResponseWriter
	opts       Options
	claims     mobileClaims
	sessionID  uint64
	messageID  uint64
	messageKey string
	text       *strings.Builder
}

func (m mobileStreamWriter) Write(p []byte) (int, error) {
	text := string(p)
	if m.text != nil {
		m.text.WriteString(text)
	}
	writeMobileRealtimeEvent(m.w, m.opts, m.claims, mobileSSEEvent{Type: "delta", SessionID: m.sessionID, MessageID: m.messageID, MessageKey: m.messageKey, Status: mobileMessageStreaming, Delta: text})
	return len(p), nil
}

// mobileNextTurns 分配下一对（用户、助手）轮次。
//
// 最大轮次必须问数据库。原先它扫的是 ListMessages(…, 1000) 的结果，而 normalizeLimit
// 会把 limit 静默夹到 500：超过 500 条的会话里算出来的"下一个"轮次其实**早就存在**，
// 而消息表的唯一键是 (session_id, turn_index) 且写入走 ON DUPLICATE KEY UPDATE ——
// 新消息会直接覆盖掉一条已有消息（TODO-116）。
func mobileNextTurns(ctx context.Context, svc TenantService, sessionID uint64) (uint, uint, error) {
	maxTurn, err := svc.MaxMessageTurn(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}
	return maxTurn + 1, maxTurn + 2, nil
}

func mobilePersistContext(ctx context.Context, claims mobileClaims) context.Context {
	if !errors.Is(ctx.Err(), context.Canceled) {
		return ctx
	}
	return observability.WithRequestValues(context.Background(), observability.TraceID(ctx), claims.UserID, claims.TenantKey)
}

func mobileFilterMessages(messages []mysqlstore.Message, afterTurn uint, limit int) []mysqlstore.Message {
	out := make([]mysqlstore.Message, 0, len(messages))
	for _, message := range messages {
		if afterTurn > 0 && message.TurnIndex <= afterTurn {
			continue
		}
		out = append(out, message)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// mobileRecapScanMessages 是会话详情为了找 latest_recap 而倒序回读的消息条数。
//
// 为什么是一个窗口而不是全会话：recap 行和对话交替写在同一张消息表里（role
// "recap"），没有独立的列可以直接定位，所以只能扫。但 latest_recap 只关心最近的
// 那一条，把整个会话拉回来纯属浪费。
//
// 为什么是 200：一次 user/assistant 往返占两条消息，200 条约等于最近 100 轮对话，
// 足以覆盖正常的 recap 生成节奏，同时读回来的量与列表分页的上限（500）同量级、
// 不会成为会话详情的新瓶颈。
//
// 代价要说清楚：窗口之外的 recap 看不见。一个 620 条消息的会话如果只有一条第 3 轮
// 写的 recap，新读法返回空，而旧读法能看到它。这是有意的取舍 —— 字段叫
// latest_recap，一条 600 轮之前的 recap 不是「最新」，把它当成当前会话摘要展示比
// 不展示更糟；而且旧读法的盲区随会话增长永久恶化（第 500 条之后的 recap 永远看不
// 到），新读法的盲区只取决于「多久没生成过 recap」。
//
// 彻底消掉盲区的写法是 WHERE role = 'recap' ORDER BY turn_index DESC LIMIT 1，
// 一条都不用多扫；本次按既定修法先上通用的「倒序取最近 N 条」读法，那个更窄的查询
// 留作后续（TODO-125）。
const mobileRecapScanMessages = 200

func mobileLatestRecapFromMessages(messages []mysqlstore.Message) *mobileLatestRecap {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "recap") || strings.TrimSpace(message.Content) == "" {
			continue
		}
		return &mobileLatestRecap{
			MessageID: message.ID,
			Content:   message.Content,
			Model:     message.Model,
			TurnIndex: message.TurnIndex,
			CreatedAt: message.CreatedAt,
		}
	}
	return nil
}

func mobileFilterMessagesPage(messages []mysqlstore.Message, afterTurn uint, limit int) ([]mysqlstore.Message, string, bool) {
	if limit <= 0 {
		limit = 50
	}
	out := make([]mysqlstore.Message, 0, limit)
	hasMore := false
	for _, message := range messages {
		if afterTurn > 0 && message.TurnIndex <= afterTurn {
			continue
		}
		if len(out) >= limit {
			hasMore = true
			break
		}
		out = append(out, message)
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		nextCursor = mobileEncodeCursor(out[len(out)-1].TurnIndex)
	}
	return out, nextCursor, hasMore
}

func mobileEncodeCursor(turn uint) string {
	if turn == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(turn), 10)
}

func mobileDecodeCursor(value string) (uint, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, true
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(cursor), true
}

func mobileMessageIDParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("message_id"), 10, 64)
	if err != nil || id == 0 {
		mobileGinError(c, http.StatusBadRequest, "message id is required")
		return 0, false
	}
	return id, true
}

func mobileFindMessageByID(messages []mysqlstore.Message, id uint64) (mysqlstore.Message, bool) {
	for _, message := range messages {
		if message.ID == id {
			return message, true
		}
	}
	return mysqlstore.Message{}, false
}

func mobileMessageMetadataFromJSON(value string) mobileMessageMetadata {
	var raw struct {
		Mobile mobileMessageMetadata `json:"mobile"`
	}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return mobileMessageMetadata{}
	}
	return raw.Mobile
}

func mobileReplayMessageSSE(w http.ResponseWriter, sessionID uint64, message mysqlstore.Message, messageKey string) {
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	metadata := mobileMessageMetadataFromJSON(message.ContentJSON)
	status := metadata.Status
	if status == "" {
		status = mobileMessageCompleted
	}
	writeMobileSSE(w, mobileSSEEvent{Type: "message_start", SessionID: sessionID, MessageID: message.ID, MessageKey: messageKey, Status: status})
	if message.Content != "" {
		writeMobileSSE(w, mobileSSEEvent{Type: "delta", SessionID: sessionID, MessageID: message.ID, MessageKey: messageKey, Status: status, Delta: message.Content})
	}
	if status == mobileMessageFailed || status == mobileMessageCancelled {
		messageError := metadata.Error
		if messageError == "" && status == mobileMessageCancelled {
			messageError = "stream cancelled"
		}
		writeMobileSSE(w, mobileSSEEvent{Type: "error", SessionID: sessionID, MessageID: message.ID, MessageKey: messageKey, Status: status, Error: messageError})
		return
	}
	writeMobileSSE(w, mobileSSEEvent{Type: "message_stop", SessionID: sessionID, MessageID: message.ID, MessageKey: messageKey, Status: status})
}

func mobileValidateAttachments(attachments []mobileAttachment, policy mobileEffectivePolicy) error {
	if len(attachments) > policy.MaxAttachmentsCount {
		return fmt.Errorf("too many attachments: max %d", policy.MaxAttachmentsCount)
	}
	allowed := make(map[string]bool, len(policy.AllowedAttachments))
	for _, value := range policy.AllowedAttachments {
		allowed[value] = true
	}
	for i := range attachments {
		attachmentType := strings.TrimSpace(attachments[i].Type)
		if attachmentType == "" {
			return errors.New("attachment type is required")
		}
		if !allowed[attachmentType] {
			return fmt.Errorf("attachment type %q is not allowed", attachmentType)
		}
		if attachments[i].SizeBytes < 0 {
			return errors.New("attachment size_bytes must be non-negative")
		}
		if policy.MaxAttachmentBytes > 0 && attachments[i].SizeBytes > policy.MaxAttachmentBytes {
			return fmt.Errorf("attachment %q exceeds max size", attachments[i].Name)
		}
		if strings.TrimSpace(attachments[i].AttachmentID) == "" {
			attachments[i].AttachmentID = mobileGeneratedKey("att")
		}
		if strings.TrimSpace(attachments[i].URL) == "" && strings.TrimSpace(attachments[i].Transcript) == "" {
			return errors.New("attachment url or transcript is required")
		}
	}
	return nil
}

func mobileValidateAttachmentUpload(req mobileAttachmentPresignRequest, policy mobileEffectivePolicy) error {
	attachmentType := strings.TrimSpace(req.Type)
	if attachmentType == "" {
		return errors.New("attachment type is required")
	}
	allowed := make(map[string]bool, len(policy.AllowedAttachments))
	for _, value := range policy.AllowedAttachments {
		allowed[value] = true
	}
	if !allowed[attachmentType] {
		return fmt.Errorf("attachment type %q is not allowed", attachmentType)
	}
	if req.SizeBytes <= 0 {
		return errors.New("attachment size_bytes must be positive")
	}
	if policy.MaxAttachmentBytes > 0 && req.SizeBytes > policy.MaxAttachmentBytes {
		return fmt.Errorf("attachment %q exceeds max size", req.Name)
	}
	return nil
}

func mobileAttachmentObjectKey(claims mobileClaims, attachmentID, name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "/", "_"))
	if name == "" {
		name = "upload"
	}
	return strings.Trim(claims.TenantKey, "/") + "/" + strings.Trim(claims.UserID, "/") + "/" + attachmentID + "/" + name
}

func mobileAttachmentURL(baseURL, objectKey string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "mobile-upload://" + objectKey
	}
	return baseURL + "/" + objectKey
}

func mobilePromptWithAttachments(content string, attachments []mobileAttachment) string {
	content = strings.TrimSpace(content)
	if len(attachments) == 0 {
		return content
	}
	var b strings.Builder
	if content != "" {
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	b.WriteString("Attached context:\n")
	for _, attachment := range attachments {
		fmt.Fprintf(&b, "- %s", attachment.Type)
		if attachment.Name != "" {
			fmt.Fprintf(&b, " name=%q", attachment.Name)
		}
		if attachment.MediaType != "" {
			fmt.Fprintf(&b, " media_type=%q", attachment.MediaType)
		}
		if attachment.URL != "" {
			fmt.Fprintf(&b, " url=%q", attachment.URL)
		}
		if attachment.Transcript != "" {
			fmt.Fprintf(&b, " transcript=%q", attachment.Transcript)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func mobileApproxAttachmentTokens(attachments []mobileAttachment) int {
	var total int
	for _, attachment := range attachments {
		total += mobileApproxTokens(attachment.Name)
		total += mobileApproxTokens(attachment.URL)
		total += mobileApproxTokens(attachment.Transcript)
	}
	return total
}

func parseMobileJWT(token, secret string, now time.Time) (mobileClaims, error) {
	if strings.TrimSpace(token) == "" {
		return mobileClaims{}, errors.New("token is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return mobileClaims{}, errors.New("invalid jwt shape")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return mobileClaims{}, err
	}
	var header mobileJWTHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return mobileClaims{}, err
	}
	if header.Algorithm != "HS256" {
		return mobileClaims{}, errors.New("unsupported jwt algorithm")
	}
	signingInput := parts[0] + "." + parts[1]
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(signingInput))
	want := base64.RawURLEncoding.EncodeToString(expected.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return mobileClaims{}, errors.New("invalid jwt signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return mobileClaims{}, err
	}
	var claims mobileClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return mobileClaims{}, err
	}
	if claims.UserID == "" {
		claims.UserID = claims.Subject
	}
	if strings.TrimSpace(claims.TenantKey) == "" || strings.TrimSpace(claims.UserID) == "" {
		return mobileClaims{}, errors.New("tenant_key and user_id are required")
	}
	if claims.ExpiresAt != 0 && now.Unix() >= claims.ExpiresAt {
		return mobileClaims{}, errors.New("jwt expired")
	}
	return claims, nil
}

func writeMobileSSE(w http.ResponseWriter, event mobileSSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\n", event.Type)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeMobileRealtimeEvent(w http.ResponseWriter, opts Options, claims mobileClaims, event mobileSSEEvent) {
	writeMobileSSE(w, event)
	// SSE remains the primary response channel; WebSocket fanout mirrors the
	// same event envelope so other signed-in devices can refresh in real time.
	if opts.MobileStreams != nil {
		opts.MobileStreams.BroadcastFor(claims, event)
	}
}

func mobileGeneratedKey(prefix string) string {
	return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func mobileMetadataJSON(claims mobileClaims, messageKey, status string) string {
	return mobileMetadataJSONWithError(claims, messageKey, status, "")
}

func mobileMetadataJSONWithError(claims mobileClaims, messageKey, status, messageError string) string {
	return mobileMetadataJSONWithAttachments(claims, messageKey, status, messageError, nil)
}

func mobileMetadataJSONWithAttachments(claims mobileClaims, messageKey, status, messageError string, attachments []mobileAttachment) string {
	data, err := json.Marshal(map[string]any{
		"mobile": map[string]any{
			"message_key": messageKey,
			"status":      status,
			"device_id":   claims.DeviceID,
			"error":       messageError,
			"attachments": attachments,
		},
	})
	if err != nil {
		return ""
	}
	return string(data)
}

func mobileMaybeGenerateTitleAsync(ctx context.Context, opts Options, session mysqlstore.Session, prompt, response string) {
	if opts.SessionTitleFunc == nil || opts.TenantService == nil || strings.TrimSpace(session.Title) != "" {
		return
	}
	sessionID := session.ID
	// WithoutCancel 保留 trace / tenant / telemetry emitter，同时脱开请求的取消与 deadline。
	detached := context.WithoutCancel(ctx)
	goSafe(detached, "server.mobileMaybeGenerateTitleAsync", map[string]any{"session_id": sessionID}, func() {
		titleCtx, cancel := context.WithTimeout(detached, 30*time.Second)
		defer cancel()
		title, err := opts.SessionTitleFunc(titleCtx, prompt, response)
		if err != nil {
			observability.Error(titleCtx, nil, "mobile.session.title_error", "server.mobileMaybeGenerateTitleAsync", "generate mobile session title failed", "session_id", sessionID, "error", err)
			return
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80])
		}
		if err := opts.TenantService.UpdateSession(titleCtx, sessionID, tenantservice.SessionRequest{Title: title}); err != nil {
			observability.Error(titleCtx, nil, "mobile.session.title_error", "server.mobileMaybeGenerateTitleAsync", "update mobile session title failed", "session_id", sessionID, "error", err)
			return
		}
		observability.Info(titleCtx, nil, "mobile.session.title_updated", "server.mobileMaybeGenerateTitleAsync", "updated mobile session title", "session_id", sessionID)
	})
}
