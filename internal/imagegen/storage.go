package imagegen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

var (
	ErrBlobNotFound                   = errors.New("imagegen blob not found")
	ErrBlobForbidden                  = errors.New("imagegen blob access forbidden")
	ErrInvalidBlob                    = errors.New("imagegen invalid blob")
	ErrFilesystemBlobStoreUnsupported = errors.New("imagegen filesystem blob store unsupported on this platform")
)

type Blob struct {
	Key       string    `json:"key"`
	MediaType string    `json:"media_type"`
	Name      string    `json:"name,omitempty"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type PutBlobRequest struct {
	Policy    media.AccessPolicy
	Key       string
	MediaType string
	Name      string
	Data      io.Reader
}

// BlobReader exposes the blob stream and metadata without exposing a local path.
type BlobReader struct {
	io.ReadCloser
	Blob Blob
}

type BlobStore interface {
	Put(context.Context, PutBlobRequest) (Blob, error)
	Open(context.Context, media.AccessPolicy, string) (BlobReader, error)
	Delete(context.Context, media.AccessPolicy, string) error
}

// BlobInventory is deliberately tenant-scoped so garbage collection cannot
// discover, compare, or delete another tenant's objects.
type BlobInventory interface {
	ListTenantBlobs(context.Context, uint64) ([]Blob, error)
}

func validatePut(req PutBlobRequest) error {
	if req.Policy.TenantID == 0 || req.Policy.UserID == 0 || req.Policy.SessionID == 0 || req.Data == nil {
		return ErrInvalidBlob
	}
	if err := validateKeyScope(req.Policy, req.Key); err != nil {
		return err
	}
	return nil
}

var scopeKeyPattern = regexp.MustCompile(`^tenant-([0-9]+)/user-([0-9]+)/session-([0-9]+)/.+$`)

// validateBlobKey is the only key normalizer for every blob-store operation.
// Blob keys are slash-delimited identifiers, not local filesystem paths.
func validateBlobKey(key string) error {
	if strings.TrimSpace(key) == "" || strings.ContainsRune(key, 0) || strings.Contains(key, "\\") || path.IsAbs(key) || path.Clean(key) != key {
		return ErrInvalidBlob
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return ErrInvalidBlob
		}
	}
	return nil
}

func validateKeyScope(policy media.AccessPolicy, key string) error {
	if err := validateBlobKey(key); err != nil {
		return err
	}
	m := scopeKeyPattern.FindStringSubmatch(key)
	if len(m) != 4 {
		return ErrInvalidBlob
	}
	tenant, _ := strconv.ParseUint(m[1], 10, 64)
	user, _ := strconv.ParseUint(m[2], 10, 64)
	session, _ := strconv.ParseUint(m[3], 10, 64)
	if tenant != policy.TenantID || user != policy.UserID || session != policy.SessionID {
		return ErrBlobForbidden
	}
	return nil
}

func validateTenantKey(tenantID uint64, key string) error {
	if tenantID == 0 {
		return ErrInvalidBlob
	}
	if err := validateBlobKey(key); err != nil {
		return err
	}
	m := scopeKeyPattern.FindStringSubmatch(key)
	if len(m) != 4 {
		return ErrInvalidBlob
	}
	tenant, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return ErrInvalidBlob
	}
	if tenant != tenantID {
		return ErrBlobForbidden
	}
	return nil
}

func hashReader(dst io.Writer, src io.Reader) (size int64, digest string, err error) {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), src)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

type memoryBlob struct {
	Blob
	Policy media.AccessPolicy
	Data   []byte
}

type MemoryBlobStore struct {
	mu   sync.RWMutex
	data map[string]memoryBlob
}

func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{data: make(map[string]memoryBlob)}
}

func (s *MemoryBlobStore) Put(ctx context.Context, req PutBlobRequest) (Blob, error) {
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}
	if err := validatePut(req); err != nil {
		return Blob{}, err
	}
	data, err := io.ReadAll(req.Data)
	if err != nil {
		return Blob{}, err
	}
	digest := sha256.Sum256(data)
	blob := Blob{Key: req.Key, MediaType: strings.TrimSpace(req.MediaType), Name: strings.TrimSpace(req.Name), SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	s.data[req.Key] = memoryBlob{Blob: blob, Policy: req.Policy, Data: append([]byte(nil), data...)}
	s.mu.Unlock()
	return blob, nil
}

func (s *MemoryBlobStore) Open(ctx context.Context, policy media.AccessPolicy, key string) (BlobReader, error) {
	if err := ctx.Err(); err != nil {
		return BlobReader{}, err
	}
	if err := validateKeyScope(policy, key); err != nil {
		if errors.Is(err, ErrBlobForbidden) {
			return BlobReader{}, err
		}
		return BlobReader{}, ErrInvalidBlob
	}
	s.mu.RLock()
	item, ok := s.data[key]
	s.mu.RUnlock()
	if !ok {
		return BlobReader{}, ErrBlobNotFound
	}
	if item.Policy != policy {
		return BlobReader{}, ErrBlobForbidden
	}
	return BlobReader{ReadCloser: io.NopCloser(strings.NewReader(string(item.Data))), Blob: item.Blob}, nil
}

func (s *MemoryBlobStore) Delete(ctx context.Context, policy media.AccessPolicy, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKeyScope(policy, key); err != nil {
		if errors.Is(err, ErrBlobForbidden) {
			return err
		}
		return ErrInvalidBlob
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.data[key]
	if !ok {
		return ErrBlobNotFound
	}
	if item.Policy != policy {
		return ErrBlobForbidden
	}
	delete(s.data, key)
	return nil
}

func (s *MemoryBlobStore) ListTenantBlobs(ctx context.Context, tenantID uint64) ([]Blob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenantID == 0 {
		return nil, ErrInvalidBlob
	}
	s.mu.RLock()
	blobs := make([]Blob, 0)
	for key, item := range s.data {
		if item.Policy.TenantID == tenantID && validateTenantKey(tenantID, key) == nil {
			blobs = append(blobs, item.Blob)
		}
	}
	s.mu.RUnlock()
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Key < blobs[j].Key })
	return blobs, nil
}

type FilesystemBlobStore struct{ root string }

func NewFilesystemBlobStore(root string) (*FilesystemBlobStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, ErrInvalidBlob
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err == nil && !info.IsDir() {
		return nil, fmt.Errorf("blob root is not a directory")
	}
	if os.IsNotExist(err) {
		if err := os.MkdirAll(root, 0o750); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	return &FilesystemBlobStore{root: resolvedRoot}, nil
}

func (s *FilesystemBlobStore) Put(ctx context.Context, req PutBlobRequest) (Blob, error) {
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}
	if err := validatePut(req); err != nil {
		return Blob{}, err
	}
	return filesystemPut(s.root, req)
}

func (s *FilesystemBlobStore) Open(ctx context.Context, policy media.AccessPolicy, key string) (BlobReader, error) {
	if err := ctx.Err(); err != nil {
		return BlobReader{}, err
	}
	if err := validateKeyScope(policy, key); err != nil {
		return BlobReader{}, err
	}
	return filesystemOpen(s.root, policy, key)
}

func (s *FilesystemBlobStore) Delete(ctx context.Context, policy media.AccessPolicy, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKeyScope(policy, key); err != nil {
		return err
	}
	return filesystemDelete(s.root, policy, key)
}

func (s *FilesystemBlobStore) ListTenantBlobs(ctx context.Context, tenantID uint64) ([]Blob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenantID == 0 {
		return nil, ErrInvalidBlob
	}
	return filesystemListTenantBlobs(ctx, s.root, tenantID)
}
