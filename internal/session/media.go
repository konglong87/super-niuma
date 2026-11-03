package session

// 会话里的二进制媒体（目前只有 MCP 工具返回的图片）的落盘形式。
//
// 为什么不把 base64 直接写进 transcript：transcript 是逐行 append 的会话日志，
// 会被 Load / LoadConversation / recap / rewind 反复整文件读回来。一张截图的
// base64 可以到 6.7MB（mcp 侧单图上限），内联进去等于让每一次读会话都拖着它走，
// 而且一行 JSON 就可能超过 LoadWithFormat 的 8MB 扫描缓冲。
//
// 所以沿用 internal/toolresult 对大工具结果的那套做法：载荷外化到 transcript
// 旁边的侧车文件，日志里只留一条引用。文件名取载荷的 sha256，因此同一张图重复
// 记录只落一个文件，也不需要计数器或随机数来起名。
//
// 存的是 base64 文本而不是解码后的字节：回放时要原样塞回 ContentSource.Data，
// 少一轮编解码就少一处可能改变字节的地方。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MediaRef 是一条 image 记录在 transcript 里留下的全部内容。它必须小到能安心放进
// 一行 JSON —— 载荷在 Path 指向的文件里。
type MediaRef struct {
	MediaType   string `json:"media_type"`
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Base64Bytes int    `json:"base64_bytes"`
}

func (r MediaRef) valid() bool {
	return strings.TrimSpace(r.Path) != "" && strings.TrimSpace(r.SHA256) != ""
}

// MarshalMediaRef 把引用编码成 Entry.Metadata 能装的 JSON。
func MarshalMediaRef(ref MediaRef) (json.RawMessage, error) {
	encoded, err := json.Marshal(ref)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

// UnmarshalMediaRef 从 Entry.Metadata 取回引用。
func UnmarshalMediaRef(raw json.RawMessage) (MediaRef, error) {
	if len(raw) == 0 {
		return MediaRef{}, fmt.Errorf("entry carries no media reference")
	}
	var ref MediaRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return MediaRef{}, err
	}
	if !ref.valid() {
		return MediaRef{}, fmt.Errorf("incomplete media reference")
	}
	return ref, nil
}

// PersistMedia 把 base64 载荷写到 <transcript 所在目录>/<sessionID>/media/<sha256>.b64，
// 返回可以放进 transcript 的引用。
func PersistMedia(sessionID, transcriptPath, mediaType, base64Data string) (MediaRef, error) {
	sessionID = strings.TrimSpace(sessionID)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if sessionID == "" || transcriptPath == "" {
		return MediaRef{}, fmt.Errorf("missing session transcript")
	}
	if base64Data == "" {
		return MediaRef{}, fmt.Errorf("missing media payload")
	}
	sum := sha256.Sum256([]byte(base64Data))
	digest := hex.EncodeToString(sum[:])
	dir := filepath.Join(filepath.Dir(transcriptPath), safeMediaPathPart(sessionID), "media")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return MediaRef{}, err
	}
	path := filepath.Join(dir, digest+".b64")
	// 内容寻址：同一份载荷已经落过盘就不重写，省掉重复的 MB 级写入。
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(base64Data)) {
		return MediaRef{MediaType: mediaType, Path: path, SHA256: digest, Base64Bytes: len(base64Data)}, nil
	}
	if err := os.WriteFile(path, []byte(base64Data), 0600); err != nil {
		return MediaRef{}, err
	}
	return MediaRef{MediaType: mediaType, Path: path, SHA256: digest, Base64Bytes: len(base64Data)}, nil
}

// LoadMedia 读回 base64 载荷，并校验它仍然对得上文件名里的哈希。校验失败按读取
// 失败处理：调用方（resume）会降级成占位文本，而不是把一份改坏的载荷发给模型。
func LoadMedia(ref MediaRef) (string, error) {
	if !ref.valid() {
		return "", fmt.Errorf("incomplete media reference")
	}
	data, err := os.ReadFile(ref.Path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if digest := hex.EncodeToString(sum[:]); digest != ref.SHA256 {
		return "", fmt.Errorf("media checksum mismatch for %s", ref.Path)
	}
	return string(data), nil
}

func safeMediaPathPart(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "unknown"
	}
	return out
}
