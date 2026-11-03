package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/quota"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func mobileJWTMiddleware(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(opts.MobileJWTSecret) == "" {
			if opts.MobileDevAuth {
				claims := mobileDevClaims(c)
				if claims.TenantKey == "" || claims.UserID == "" {
					mobileGinError(c, http.StatusUnauthorized, "mobile dev auth requires X-Tenant-Key and X-User-Id")
					return
				}
				ctx := observability.WithRequestValues(c.Request.Context(), observability.TraceID(c.Request.Context()), claims.UserID, claims.TenantKey)
				ctx = mobileTelemetryContext(ctx, opts)
				c.Request = c.Request.WithContext(ctx)
				c.Set("mobileClaims", claims)
				c.Set("mobilePolicy", mobilePolicyForClaims(opts.MobilePolicy, claims))
				c.Next()
				return
			}
			mobileGinError(c, http.StatusServiceUnavailable, "mobile jwt secret is not configured")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		claims, err := parseMobileJWT(token, opts.MobileJWTSecret, time.Now())
		if err != nil {
			mobileGinError(c, http.StatusUnauthorized, "invalid mobile token")
			return
		}
		ctx := observability.WithRequestValues(c.Request.Context(), observability.TraceID(c.Request.Context()), claims.UserID, claims.TenantKey)
		ctx = mobileTelemetryContext(ctx, opts)
		c.Request = c.Request.WithContext(ctx)
		c.Set("mobileClaims", claims)
		c.Set("mobilePolicy", mobilePolicyForClaims(opts.MobilePolicy, claims))
		c.Next()
	}
}

func mobileTelemetryContext(ctx context.Context, opts Options) context.Context {
	sinks := []telemetry.Sink{telemetry.LoggerSink{}}
	if opts.TelemetryMetrics != nil {
		sinks = append(sinks, opts.TelemetryMetrics)
	}
	// 移动端已在中间件里鉴过权，写库 sink 无条件挂上；异步缓冲同上。
	sinks = append(sinks, telemetrySinksFor(opts, true)...)
	return telemetry.WithEmitter(ctx, telemetry.NewEmitter(sinks...))
}

func mobileDevClaims(c *gin.Context) mobileClaims {
	return mobileClaims{
		TenantKey: strings.TrimSpace(c.GetHeader("X-Tenant-Key")),
		UserID:    strings.TrimSpace(c.GetHeader("X-User-Id")),
		DeviceID:  strings.TrimSpace(c.GetHeader("X-Device-Id")),
	}
}

func mobileAccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		claims := mobileClaimsFromGin(c)
		observability.Info(c.Request.Context(), nil, "mobile.request.start", "server.mobileAccessLogMiddleware", "mobile request start", mobileAccessLogStartAttrs(c, claims, start)...)
		c.Next()
		end := time.Now()
		attrs := mobileAccessLogFinishAttrs(c, claims, start, end)
		if c.Writer.Status() >= http.StatusInternalServerError {
			observability.Error(c.Request.Context(), nil, "mobile.request.finish", "server.mobileAccessLogMiddleware", "mobile request finish", attrs...)
			return
		}
		observability.Info(c.Request.Context(), nil, "mobile.request.finish", "server.mobileAccessLogMiddleware", "mobile request finish", attrs...)
	}
}

// mobileBodyLogSampleMaxBytes 是日志摘要最多缓存的字节数。原先这里是无上限的
// io.ReadAll —— 一个大请求体会被整体读进内存只为了打一行日志。现在只采样开头，
// 剩下的原样流给 handler，日志层不再是内存放大器。
const mobileBodyLogSampleMaxBytes = 64 << 10

func mobileBodySummaryLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && methodMayHaveBody(c.Request.Method) {
			original := c.Request.Body
			sample, err := io.ReadAll(io.LimitReader(original, mobileBodyLogSampleMaxBytes))
			if err == nil {
				// 采样过的部分回填到 body 前面，handler 仍然看到完整请求体。
				c.Request.Body = &replayReadCloser{
					Reader: io.MultiReader(bytes.NewReader(sample), original),
					closer: original,
				}
				truncated := len(sample) == mobileBodyLogSampleMaxBytes
				observability.Debug(c.Request.Context(), nil, "mobile.request_body", "server.mobileBodySummaryLogMiddleware", "mobile request body summary", mobileBodySummaryAttrs(sample, truncated)...)
			}
		}
		c.Next()
	}
}

// replayReadCloser 把「采样片段 + 剩余流」拼回一个 ReadCloser，Close 仍然落到原 body。
type replayReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *replayReadCloser) Close() error { return r.closer.Close() }

func mobileClaimsFromGin(c *gin.Context) mobileClaims {
	if value, ok := c.Get("mobileClaims"); ok {
		if claims, ok := value.(mobileClaims); ok {
			return claims
		}
	}
	return mobileClaims{}
}

func mobilePolicyFromGin(c *gin.Context) mobileEffectivePolicy {
	if value, ok := c.Get("mobilePolicy"); ok {
		if policy, ok := value.(mobileEffectivePolicy); ok {
			return policy
		}
	}
	return mobilePolicyForClaims(MobilePolicy{}, mobileClaims{})
}

func mobileRequireTenantService(c *gin.Context, opts Options) bool {
	if opts.TenantService == nil {
		mobileGinError(c, http.StatusServiceUnavailable, "tenant storage is not configured")
		return false
	}
	return true
}

func mobileSessionIDParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		mobileGinError(c, http.StatusBadRequest, "session id is required")
		return 0, false
	}
	return id, true
}

func methodMayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

func mobileBodySummaryAttrs(body []byte, truncated bool) []any {
	attrs := []any{"body_bytes", len(body), "body_truncated", truncated}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return append(attrs, "json", false)
	}
	attrs = append(attrs, "json", true)
	if model, ok := raw["model"].(string); ok && model != "" {
		attrs = append(attrs, "model", model)
	}
	if sessionKey, ok := raw["session_key"].(string); ok && sessionKey != "" {
		attrs = append(attrs, "session_key", sessionKey)
	}
	if content, ok := raw["content"].(string); ok {
		attrs = append(attrs, "content_chars", len([]rune(content)), "has_content", strings.TrimSpace(content) != "")
	}
	if title, ok := raw["title"].(string); ok {
		attrs = append(attrs, "title_chars", len([]rune(title)))
	}
	if attachments, ok := raw["attachments"].([]any); ok {
		attrs = append(attrs, "attachment_count", len(attachments))
	}
	if _, ok := raw["metadata_json"]; ok {
		attrs = append(attrs, "has_metadata_json", true)
	}
	return attrs
}

func mobileAccessLogStartAttrs(c *gin.Context, claims mobileClaims, start time.Time) []any {
	attrs := mobileBaseLogAttrs(c, claims)
	attrs = append(attrs,
		"phase", "start",
		"started_at", start.Format(time.RFC3339Nano),
	)
	return attrs
}

func mobileAccessLogFinishAttrs(c *gin.Context, claims mobileClaims, start, end time.Time) []any {
	attrs := mobileBaseLogAttrs(c, claims)
	attrs = append(attrs,
		"phase", "finish",
		"started_at", start.Format(time.RFC3339Nano),
		"ended_at", end.Format(time.RFC3339Nano),
		"latency_ms", end.Sub(start).Milliseconds(),
		"status", c.Writer.Status(),
		"response_bytes", c.Writer.Size(),
		"aborted", c.IsAborted(),
		"error_count", len(c.Errors),
	)
	if len(c.Errors) > 0 {
		attrs = append(attrs, "gin_errors", c.Errors.String())
	}
	return attrs
}

func mobileBaseLogAttrs(c *gin.Context, claims mobileClaims) []any {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	return []any{
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"route", c.FullPath(),
		"query", c.Request.URL.RawQuery,
		"remote_addr", c.ClientIP(),
		"user_agent", c.Request.UserAgent(),
		"user_id", claims.UserID,
		"tenant_id", claims.TenantID,
		"tenant_key", claims.TenantKey,
		"device_id", claims.DeviceID,
		"session_id", sessionID,
	}
}

func mobileGinError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

func mobileGinTenantServiceError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, tenantservice.ErrMissingTenantKey):
		mobileGinError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, tenantservice.ErrMissingUserID):
		mobileGinError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, tenantservice.ErrForbidden):
		mobileGinError(c, http.StatusForbidden, err.Error())
	case errors.Is(err, mysqlstore.ErrNotFound):
		mobileGinError(c, http.StatusNotFound, err.Error())
	case errors.Is(err, mysqlstore.ErrTooManyMessages):
		// 请求本身合法，是这个会话大到无法一次性 fork。422 而不是 500：
		// 这是可以告知用户的确定性拒绝，不是服务端故障。
		mobileGinError(c, http.StatusUnprocessableEntity, err.Error())
	default:
		mobileGinError(c, http.StatusInternalServerError, err.Error())
	}
}

// mobileGinMessageLookupError 把消息定点查询的失败翻成响应。
//
// GetMessage / PreviousUserMessage 查不到时报 ErrNotFound，但这两处原先是在一个
// 内存切片里线性查找、自己拼的错误正文（404 message not found、400 previous user
// message not found）。换成定点查询不能顺带改掉客户端看到的状态码和正文，
// 也不该把仓储的错误文本原样透出去，所以状态码和正文由调用方给。
func mobileGinMessageLookupError(c *gin.Context, err error, status int, message string) {
	if errors.Is(err, mysqlstore.ErrNotFound) {
		mobileGinError(c, status, message)
		return
	}
	mobileGinTenantServiceError(c, err)
}

func mobileGinPolicyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errMobileModelForbidden):
		mobileGinError(c, http.StatusForbidden, err.Error())
	case errors.Is(err, errMobileRateLimited), errors.Is(err, quota.ErrRateLimited), errors.Is(err, quota.ErrConcurrentLimitExceeded):
		mobileGinError(c, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, errMobileQuotaExceeded), errors.Is(err, quota.ErrDailyTokenLimitExceeded), errors.Is(err, quota.ErrDailyMessageLimitExceeded):
		mobileGinError(c, http.StatusPaymentRequired, err.Error())
	default:
		mobileGinError(c, http.StatusForbidden, err.Error())
	}
}
