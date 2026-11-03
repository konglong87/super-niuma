package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	errMobileModelForbidden = errors.New("model is not allowed for this user")
	errMobileRateLimited    = errors.New("mobile rate limit exceeded")
	errMobileQuotaExceeded  = errors.New("mobile quota exceeded")
)

type MobilePolicy struct {
	AllowedModels       []string
	RateLimitPerMinute  int
	DailyMessageQuota   int
	DailyTokenQuota     int
	MaxAttachmentBytes  int64
	AllowedAttachments  []string
	MaxAttachmentsCount int
}

type mobileEffectivePolicy struct {
	AllowedModels       []string
	RateLimitPerMinute  int
	DailyMessageQuota   int
	DailyTokenQuota     int
	MaxAttachmentBytes  int64
	AllowedAttachments  []string
	MaxAttachmentsCount int
}

type MobileUsagePolicy = mobileEffectivePolicy

type MobileUsageStore interface {
	Reserve(userKey string, policy MobileUsagePolicy, estimatedTokens int) error
	AddTokens(userKey string, tokens int)
}

type mobileUsageReleaser interface {
	Release(userKey string, estimatedTokens int)
}

type MobileLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	users map[string]*mobileUsage
}

type mobileUsage struct {
	minuteWindow time.Time
	minuteCount  int
	dayWindow    time.Time
	messageCount int
	tokenCount   int
}

type mobileUsageSnapshot struct {
	MinuteWindow time.Time `json:"minute_window"`
	MinuteCount  int       `json:"minute_count"`
	DayWindow    time.Time `json:"day_window"`
	MessageCount int       `json:"message_count"`
	TokenCount   int       `json:"token_count"`
}

func NewMobileLimiter(now func() time.Time) *MobileLimiter {
	if now == nil {
		now = time.Now
	}
	return &MobileLimiter{now: now, users: make(map[string]*mobileUsage)}
}

func (l *MobileLimiter) Reserve(userKey string, policy mobileEffectivePolicy, estimatedTokens int) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	usage := l.userUsage(userKey)
	now := l.now()
	minute := now.Truncate(time.Minute)
	if !usage.minuteWindow.Equal(minute) {
		usage.minuteWindow = minute
		usage.minuteCount = 0
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !usage.dayWindow.Equal(day) {
		usage.dayWindow = day
		usage.messageCount = 0
		usage.tokenCount = 0
	}
	if policy.RateLimitPerMinute > 0 && usage.minuteCount >= policy.RateLimitPerMinute {
		return errMobileRateLimited
	}
	if policy.DailyMessageQuota > 0 && usage.messageCount >= policy.DailyMessageQuota {
		return errMobileQuotaExceeded
	}
	if policy.DailyTokenQuota > 0 && usage.tokenCount+estimatedTokens > policy.DailyTokenQuota {
		return errMobileQuotaExceeded
	}
	usage.minuteCount++
	usage.messageCount++
	usage.tokenCount += estimatedTokens
	return nil
}

func (l *MobileLimiter) AddTokens(userKey string, tokens int) {
	if l == nil || tokens <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.userUsage(userKey).tokenCount += tokens
}

func (l *MobileLimiter) Release(userKey string, estimatedTokens int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	releaseMobileUsage(l.userUsage(userKey), estimatedTokens)
}

type MobileUsageFileStore struct {
	mu     sync.Mutex
	path   string
	now    func() time.Time
	loaded bool
	users  map[string]*mobileUsage
}

func NewMobileUsageFileStore(path string, now func() time.Time) *MobileUsageFileStore {
	if now == nil {
		now = time.Now
	}
	return &MobileUsageFileStore{path: path, now: now, users: make(map[string]*mobileUsage)}
}

func (s *MobileUsageFileStore) Reserve(userKey string, policy mobileEffectivePolicy, estimatedTokens int) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	if err := reserveMobileUsage(s.userUsageLocked(userKey), s.now(), policy, estimatedTokens); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *MobileUsageFileStore) AddTokens(userKey string, tokens int) {
	if s == nil || tokens <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return
	}
	s.userUsageLocked(userKey).tokenCount += tokens
	_ = s.saveLocked()
}

func (s *MobileUsageFileStore) Release(userKey string, estimatedTokens int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return
	}
	releaseMobileUsage(s.userUsageLocked(userKey), estimatedTokens)
	_ = s.saveLocked()
}

func (s *MobileUsageFileStore) loadLocked() error {
	if s.loaded {
		return nil
	}
	s.loaded = true
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var snapshots map[string]mobileUsageSnapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return err
	}
	for key, snapshot := range snapshots {
		s.users[key] = &mobileUsage{
			minuteWindow: snapshot.MinuteWindow,
			minuteCount:  snapshot.MinuteCount,
			dayWindow:    snapshot.DayWindow,
			messageCount: snapshot.MessageCount,
			tokenCount:   snapshot.TokenCount,
		}
	}
	return nil
}

func (s *MobileUsageFileStore) saveLocked() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	snapshots := make(map[string]mobileUsageSnapshot, len(s.users))
	for key, usage := range s.users {
		snapshots[key] = mobileUsageSnapshot{
			MinuteWindow: usage.minuteWindow,
			MinuteCount:  usage.minuteCount,
			DayWindow:    usage.dayWindow,
			MessageCount: usage.messageCount,
			TokenCount:   usage.tokenCount,
		}
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *MobileUsageFileStore) userUsageLocked(userKey string) *mobileUsage {
	if userKey == "" {
		userKey = "anonymous"
	}
	usage := s.users[userKey]
	if usage == nil {
		usage = &mobileUsage{}
		s.users[userKey] = usage
	}
	return usage
}

func (l *MobileLimiter) userUsage(userKey string) *mobileUsage {
	if userKey == "" {
		userKey = "anonymous"
	}
	usage := l.users[userKey]
	if usage == nil {
		usage = &mobileUsage{}
		l.users[userKey] = usage
	}
	return usage
}

func reserveMobileUsage(usage *mobileUsage, now time.Time, policy mobileEffectivePolicy, estimatedTokens int) error {
	minute := now.Truncate(time.Minute)
	if !usage.minuteWindow.Equal(minute) {
		usage.minuteWindow = minute
		usage.minuteCount = 0
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !usage.dayWindow.Equal(day) {
		usage.dayWindow = day
		usage.messageCount = 0
		usage.tokenCount = 0
	}
	if policy.RateLimitPerMinute > 0 && usage.minuteCount >= policy.RateLimitPerMinute {
		return errMobileRateLimited
	}
	if policy.DailyMessageQuota > 0 && usage.messageCount >= policy.DailyMessageQuota {
		return errMobileQuotaExceeded
	}
	if policy.DailyTokenQuota > 0 && usage.tokenCount+estimatedTokens > policy.DailyTokenQuota {
		return errMobileQuotaExceeded
	}
	usage.minuteCount++
	usage.messageCount++
	usage.tokenCount += estimatedTokens
	return nil
}

func releaseMobileUsage(usage *mobileUsage, estimatedTokens int) {
	if usage == nil {
		return
	}
	if usage.minuteCount > 0 {
		usage.minuteCount--
	}
	if usage.messageCount > 0 {
		usage.messageCount--
	}
	if estimatedTokens > 0 {
		if usage.tokenCount > estimatedTokens {
			usage.tokenCount -= estimatedTokens
		} else {
			usage.tokenCount = 0
		}
	}
}

func mobilePolicyForClaims(base MobilePolicy, claims mobileClaims) mobileEffectivePolicy {
	policy := mobileEffectivePolicy{
		AllowedModels:       cleanStringList(base.AllowedModels),
		RateLimitPerMinute:  base.RateLimitPerMinute,
		DailyMessageQuota:   base.DailyMessageQuota,
		DailyTokenQuota:     base.DailyTokenQuota,
		MaxAttachmentBytes:  base.MaxAttachmentBytes,
		AllowedAttachments:  cleanStringList(base.AllowedAttachments),
		MaxAttachmentsCount: base.MaxAttachmentsCount,
	}
	if len(claims.AllowedModels) > 0 {
		policy.AllowedModels = cleanStringList(claims.AllowedModels)
	}
	if claims.RateLimitPerMinute > 0 {
		policy.RateLimitPerMinute = claims.RateLimitPerMinute
	}
	if claims.DailyMessageQuota > 0 {
		policy.DailyMessageQuota = claims.DailyMessageQuota
	}
	if claims.DailyTokenQuota > 0 {
		policy.DailyTokenQuota = claims.DailyTokenQuota
	}
	if policy.MaxAttachmentBytes <= 0 {
		policy.MaxAttachmentBytes = 25 * 1024 * 1024
	}
	if len(policy.AllowedAttachments) == 0 {
		policy.AllowedAttachments = []string{"file", "image", "audio", "voice"}
	}
	if policy.MaxAttachmentsCount <= 0 {
		policy.MaxAttachmentsCount = 8
	}
	return policy
}

func mobileValidateModel(model string, policy mobileEffectivePolicy) error {
	model = strings.TrimSpace(model)
	if model == "" || len(policy.AllowedModels) == 0 {
		return nil
	}
	for _, allowed := range policy.AllowedModels {
		if model == allowed {
			return nil
		}
	}
	return errMobileModelForbidden
}

func mobileUsageKey(claims mobileClaims) string {
	return claims.TenantKey + ":" + claims.UserID
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func mobileApproxTokens(text string) int {
	fields := strings.Fields(text)
	if len(fields) > 0 {
		return len(fields)
	}
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}
