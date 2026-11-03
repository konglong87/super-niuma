package files

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	DefaultChunkSize    int64 = 100 * 1024 * 1024
	DefaultPreviewBytes int64 = 64 * 1024
	MaxScanTokenSize          = 2 * 1024 * 1024
)

type Config struct {
	ChunkSize    int64
	PreviewBytes int64
}

type Manifest struct {
	Path         string  `json:"path"`
	SizeBytes    int64   `json:"size_bytes"`
	Large        bool    `json:"large"`
	Binary       bool    `json:"binary"`
	MIMEType     string  `json:"mime_type,omitempty"`
	ChunkSize    int64   `json:"chunk_size"`
	ChunkCount   int     `json:"chunk_count"`
	SHA256Prefix string  `json:"sha256_prefix,omitempty"`
	Chunks       []Chunk `json:"chunks,omitempty"`
}

type Chunk struct {
	Index      int   `json:"index"`
	ByteOffset int64 `json:"byte_offset"`
	ByteLength int64 `json:"byte_length"`
	LineStart  int   `json:"line_start,omitempty"`
	LineEnd    int   `json:"line_end,omitempty"`
}

type ReadRequest struct {
	ChunkIndex  int
	ByteOffset  int64
	ByteLimit   int64
	LineOffset  int
	LineLimit   int
	LineNumbers bool
}

type ReadResult struct {
	Manifest  Manifest
	Content   string
	Offset    int64
	BytesRead int64
	Truncated bool
}

type GrepMatch struct {
	LineNumber int
	Line       string
}

type GrepResult struct {
	Matches    []GrepMatch
	MatchCount int
	Binary     bool
}

type Edit struct {
	OldString  string
	NewString  string
	ReplaceAll bool
}

type ChangeSnapshot struct {
	Before             string
	After              string
	BeforeSnapshotPath string
	AfterSnapshotPath  string
}

func NormalizeConfig(cfg Config) Config {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = DefaultChunkSize
	}
	if cfg.PreviewBytes <= 0 {
		cfg.PreviewBytes = DefaultPreviewBytes
	}
	return cfg
}

func Inspect(path string, cfg Config) (Manifest, error) {
	cfg = NormalizeConfig(cfg)
	info, err := os.Stat(path)
	if err != nil {
		return Manifest{}, err
	}
	if info.IsDir() {
		return Manifest{}, fmt.Errorf("%s is a directory", path)
	}
	manifest := Manifest{
		Path:       path,
		SizeBytes:  info.Size(),
		Large:      info.Size() > cfg.ChunkSize,
		ChunkSize:  cfg.ChunkSize,
		ChunkCount: 1,
	}
	manifest.Binary, manifest.MIMEType = sniffFile(path)
	manifest.SHA256Prefix = sha256Prefix(path)
	if !manifest.Large {
		manifest.Chunks = []Chunk{{Index: 0, ByteOffset: 0, ByteLength: info.Size()}}
		return manifest, nil
	}
	chunks, err := chunkFile(path, info.Size(), cfg.ChunkSize)
	if err != nil {
		return Manifest{}, err
	}
	if !manifest.Binary {
		chunks = annotateChunkLineRanges(path, chunks)
	}
	manifest.Chunks = chunks
	manifest.ChunkCount = len(chunks)
	return manifest, nil
}

func Read(path string, req ReadRequest, cfg Config) (ReadResult, error) {
	cfg = NormalizeConfig(cfg)
	manifest, err := Inspect(path, cfg)
	if err != nil {
		return ReadResult{}, err
	}
	if manifest.Binary {
		return ReadResult{Manifest: manifest}, nil
	}
	if req.LineOffset > 0 || req.LineLimit > 0 {
		content, err := readLines(path, req.LineOffset, req.LineLimit, req.LineNumbers)
		if err != nil {
			return ReadResult{}, err
		}
		return ReadResult{Manifest: manifest, Content: content, BytesRead: int64(len(content))}, nil
	}
	offset, limit := req.ByteOffset, req.ByteLimit
	if req.ChunkIndex > 0 {
		if req.ChunkIndex > len(manifest.Chunks) {
			return ReadResult{}, fmt.Errorf("chunk_index %d out of range; file has %d chunks", req.ChunkIndex, len(manifest.Chunks))
		}
		chunk := manifest.Chunks[req.ChunkIndex-1]
		offset = chunk.ByteOffset
		limit = chunk.ByteLength
	}
	if manifest.Large && req.ChunkIndex == 0 && req.ByteLimit <= 0 && req.ByteOffset <= 0 {
		limit = cfg.PreviewBytes
	}
	if limit <= 0 || offset+limit > manifest.SizeBytes {
		limit = manifest.SizeBytes - offset
	}
	if offset < 0 || offset > manifest.SizeBytes {
		return ReadResult{}, fmt.Errorf("byte_offset %d out of range", offset)
	}
	content, bytesRead, err := readByteRange(path, offset, limit)
	if err != nil {
		return ReadResult{}, err
	}
	return ReadResult{
		Manifest:  manifest,
		Content:   content,
		Offset:    offset,
		BytesRead: bytesRead,
		Truncated: manifest.Large && bytesRead < manifest.SizeBytes && req.ChunkIndex == 0,
	}, nil
}

func Grep(path string, re *regexp.Regexp, before, after int, maxMatches int) (GrepResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return GrepResult{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxScanTokenSize)
	var (
		out        []GrepMatch
		prior      []GrepMatch
		pending    int
		lineNumber int
		matchCount int
		seen       = map[int]bool{}
	)
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.ContainsRune(line, '\x00') {
			return GrepResult{Binary: true}, nil
		}
		current := GrepMatch{LineNumber: lineNumber, Line: line}
		matched := re.MatchString(line)
		if matched {
			matchCount++
			if maxMatches >= 0 {
				for _, item := range prior {
					if !seen[item.LineNumber] {
						out = append(out, item)
						seen[item.LineNumber] = true
					}
				}
				if !seen[current.LineNumber] {
					out = append(out, current)
					seen[current.LineNumber] = true
				}
			}
			pending = after
		} else if pending > 0 && maxMatches >= 0 {
			if !seen[current.LineNumber] {
				out = append(out, current)
				seen[current.LineNumber] = true
			}
			pending--
		}
		if before > 0 {
			prior = append(prior, current)
			if len(prior) > before {
				copy(prior, prior[1:])
				prior = prior[:before]
			}
		}
		if maxMatches > 0 && len(out) >= maxMatches && pending == 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return GrepResult{}, err
	}
	return GrepResult{Matches: out, MatchCount: matchCount}, nil
}

func Replace(path, oldString, newString string, replaceAll bool) (before, after string, replacements int, err error) {
	change, replacements, err := ReplaceDetailed(path, oldString, newString, replaceAll)
	return change.Before, change.After, replacements, err
}

func ReplaceDetailed(path, oldString, newString string, replaceAll bool) (ChangeSnapshot, int, error) {
	if oldString == "" {
		return ChangeSnapshot{}, 0, errors.New("old_string must not be empty")
	}
	manifest, err := Inspect(path, Config{})
	if err != nil {
		return ChangeSnapshot{}, 0, err
	}
	if manifest.Large {
		replacements, err := countOccurrences(path, []byte(oldString))
		if err != nil {
			return ChangeSnapshot{}, 0, err
		}
		if replacements == 0 {
			return ChangeSnapshot{}, 0, fmt.Errorf("old_string was not found in %s. The file may have changed since you last read it (edited, restored, or reformatted). Re-read the relevant section with Read and copy old_string exactly before retrying; do not retry Edit with the same old_string.", path)
		}
		if !replaceAll && replacements != 1 {
			return ChangeSnapshot{}, 0, fmt.Errorf("old_string must occur exactly once, found %d matches", replacements)
		}
		beforeSnapshot, err := persistSnapshot(path)
		if err != nil {
			return ChangeSnapshot{}, 0, err
		}
		if err := streamReplaceAtomic(path, []byte(oldString), []byte(newString), replaceAll); err != nil {
			return ChangeSnapshot{}, 0, err
		}
		next, _ := Inspect(path, Config{})
		afterSnapshot, err := persistSnapshot(path)
		if err != nil {
			return ChangeSnapshot{}, 0, err
		}
		return ChangeSnapshot{
			Before:             largePlaceholder(path, manifest),
			After:              largePlaceholder(path, next),
			BeforeSnapshotPath: beforeSnapshot,
			AfterSnapshotPath:  afterSnapshot,
		}, replacements, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ChangeSnapshot{}, 0, err
	}
	before := string(data)
	replacements := strings.Count(before, oldString)
	if replacements == 0 {
		return ChangeSnapshot{}, 0, fmt.Errorf("old_string was not found in %s. The file may have changed since you last read it (edited, restored, or reformatted). Re-read the relevant section with Read and copy old_string exactly before retrying; do not retry Edit with the same old_string.", path)
	}
	if !replaceAll && replacements != 1 {
		return ChangeSnapshot{}, 0, fmt.Errorf("old_string must occur exactly once, found %d matches", replacements)
	}
	n := 1
	if replaceAll {
		n = -1
	}
	after := strings.Replace(before, oldString, newString, n)
	err = writeAtomic(path, []byte(after), existingFileMode(path, 0644))
	return ChangeSnapshot{Before: before, After: after}, replacements, err
}

func MultiReplace(path string, edits []Edit) (before, after string, replacements int, err error) {
	change, replacements, err := MultiReplaceDetailed(path, edits)
	return change.Before, change.After, replacements, err
}

func MultiReplaceDetailed(path string, edits []Edit) (ChangeSnapshot, int, error) {
	if len(edits) == 0 {
		return ChangeSnapshot{}, 0, errors.New("edits must contain at least one edit")
	}
	manifest, err := Inspect(path, Config{})
	if err != nil {
		return ChangeSnapshot{}, 0, err
	}
	if !manifest.Large {
		data, err := os.ReadFile(path)
		if err != nil {
			return ChangeSnapshot{}, 0, err
		}
		text := string(data)
		before := text
		replacements := 0
		for i, edit := range edits {
			if edit.OldString == "" {
				return ChangeSnapshot{}, 0, fmt.Errorf("edit %d old_string must not be empty", i)
			}
			count := strings.Count(text, edit.OldString)
			if count == 0 {
				return ChangeSnapshot{}, 0, fmt.Errorf("edit %d old_string was not found in %s. The file may have changed since you last read it (edited, restored, or reformatted). Re-read with Read and copy old_string exactly before retrying; do not retry the same edit.", i, path)
			}
			if !edit.ReplaceAll && count != 1 {
				return ChangeSnapshot{}, 0, fmt.Errorf("edit %d old_string must occur exactly once, found %d matches", i, count)
			}
			n := 1
			if edit.ReplaceAll {
				n = -1
				replacements += count
			} else {
				replacements++
			}
			text = strings.Replace(text, edit.OldString, edit.NewString, n)
		}
		after := text
		return ChangeSnapshot{Before: before, After: after}, replacements, writeAtomic(path, []byte(after), existingFileMode(path, 0644))
	}
	tmp, err := copyToTemp(path)
	if err != nil {
		return ChangeSnapshot{}, 0, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	beforeSnapshot, err := persistSnapshot(path)
	if err != nil {
		return ChangeSnapshot{}, 0, err
	}
	replacements := 0
	for i, edit := range edits {
		if edit.OldString == "" {
			return ChangeSnapshot{}, 0, fmt.Errorf("edit %d old_string must not be empty", i)
		}
		count, err := countOccurrences(tmp, []byte(edit.OldString))
		if err != nil {
			return ChangeSnapshot{}, 0, err
		}
		if count == 0 {
			return ChangeSnapshot{}, 0, fmt.Errorf("edit %d old_string was not found in %s. The file may have changed since you last read it (edited, restored, or reformatted). Re-read with Read and copy old_string exactly before retrying; do not retry the same edit.", i, path)
		}
		if !edit.ReplaceAll && count != 1 {
			return ChangeSnapshot{}, 0, fmt.Errorf("edit %d old_string must occur exactly once, found %d matches", i, count)
		}
		if err := streamReplaceAtomic(tmp, []byte(edit.OldString), []byte(edit.NewString), edit.ReplaceAll); err != nil {
			return ChangeSnapshot{}, 0, err
		}
		if edit.ReplaceAll {
			replacements += count
		} else {
			replacements++
		}
	}
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, info.Mode())
	}
	if err := os.Rename(tmp, path); err != nil {
		return ChangeSnapshot{}, 0, err
	}
	cleanup = false
	next, _ := Inspect(path, Config{})
	afterSnapshot, err := persistSnapshot(path)
	if err != nil {
		return ChangeSnapshot{}, 0, err
	}
	return ChangeSnapshot{
		Before:             largePlaceholder(path, manifest),
		After:              largePlaceholder(path, next),
		BeforeSnapshotPath: beforeSnapshot,
		AfterSnapshotPath:  afterSnapshot,
	}, replacements, nil
}

func countOccurrences(path string, old []byte) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1024*1024)
	pending := make([]byte, 0, len(old)*2)
	count := 0
	buf := make([]byte, 1024*1024)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				idx := bytes.Index(pending, old)
				if idx < 0 {
					break
				}
				count++
				pending = pending[idx+len(old):]
			}
			if keep := len(old) - 1; keep > 0 && len(pending) > keep {
				pending = append([]byte(nil), pending[len(pending)-keep:]...)
			} else if keep == 0 {
				pending = pending[:0]
			}
		}
		if readErr == io.EOF {
			return count, nil
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}

func streamReplaceAtomic(path string, old, new []byte, replaceAll bool) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	in, err := os.Open(path)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	defer in.Close()
	if err := streamReplace(in, tmp, old, new, replaceAll); err != nil {
		_ = tmp.Close()
		return err
	}
	if info, err := os.Stat(path); err == nil {
		_ = tmp.Chmod(info.Mode())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func streamReplace(in io.Reader, out io.Writer, old, new []byte, replaceAll bool) error {
	reader := bufio.NewReaderSize(in, 1024*1024)
	pending := make([]byte, 0, len(old)*2)
	buf := make([]byte, 1024*1024)
	replaced := false
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				idx := bytes.Index(pending, old)
				if idx < 0 || (!replaceAll && replaced) {
					break
				}
				if _, err := out.Write(pending[:idx]); err != nil {
					return err
				}
				if _, err := out.Write(new); err != nil {
					return err
				}
				pending = pending[idx+len(old):]
				replaced = true
			}
			if keep := len(old) - 1; keep > 0 && len(pending) > keep && (replaceAll || !replaced) {
				flush := len(pending) - keep
				if _, err := out.Write(pending[:flush]); err != nil {
					return err
				}
				pending = append([]byte(nil), pending[flush:]...)
			} else if keep == 0 && (replaceAll || !replaced) {
				if _, err := out.Write(pending); err != nil {
					return err
				}
				pending = pending[:0]
			} else if !replaceAll && replaced {
				if _, err := out.Write(pending); err != nil {
					return err
				}
				pending = pending[:0]
			}
		}
		if readErr == io.EOF {
			if len(pending) > 0 {
				_, err := out.Write(pending)
				return err
			}
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func FormatManifest(result ReadResult) string {
	m := result.Manifest
	var b strings.Builder
	if m.Binary {
		fmt.Fprintf(&b, "Binary file detected: %s\n", m.Path)
	} else {
		fmt.Fprintf(&b, "Large file detected: %s\n", m.Path)
	}
	fmt.Fprintf(&b, "size_bytes: %d\n", m.SizeBytes)
	if m.MIMEType != "" {
		fmt.Fprintf(&b, "mime_type: %s\n", m.MIMEType)
	}
	fmt.Fprintf(&b, "chunk_size: %d\nchunk_count: %d\nsha256_prefix: %s\n", m.ChunkSize, m.ChunkCount, m.SHA256Prefix)
	if len(m.Chunks) > 0 {
		b.WriteString("chunks:\n")
		for _, chunk := range m.Chunks {
			fmt.Fprintf(&b, "  - index: %d byte_offset: %d byte_length: %d", chunk.Index, chunk.ByteOffset, chunk.ByteLength)
			if chunk.LineStart > 0 && chunk.LineEnd > 0 {
				fmt.Fprintf(&b, " line_start: %d line_end: %d", chunk.LineStart, chunk.LineEnd)
			}
			b.WriteString("\n")
		}
	}
	if result.Content != "" {
		fmt.Fprintf(&b, "\nPreview byte_offset=%d bytes=%d:\n%s", result.Offset, result.BytesRead, result.Content)
		if !strings.HasSuffix(result.Content, "\n") {
			b.WriteString("\n")
		}
	}
	if m.Binary {
		b.WriteString("\nBinary/media content is intentionally not inlined. Use external tooling or an attachment-aware flow to inspect the payload safely.\n")
		return b.String()
	}
	if result.Truncated {
		b.WriteString("\nUse chunk_index, byte_offset/byte_limit, or offset/limit to read the next segment.\n")
	}
	return b.String()
}

func chunkFile(path string, size, chunkSize int64) ([]Chunk, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var chunks []Chunk
	for offset := int64(0); offset < size; {
		end := offset + chunkSize
		if end >= size {
			chunks = append(chunks, Chunk{Index: len(chunks) + 1, ByteOffset: offset, ByteLength: size - offset})
			break
		}
		adjusted, err := nextLineBoundary(file, end, size)
		if err != nil {
			return nil, err
		}
		if adjusted <= offset {
			adjusted = end
		}
		chunks = append(chunks, Chunk{Index: len(chunks) + 1, ByteOffset: offset, ByteLength: adjusted - offset})
		offset = adjusted
	}
	return chunks, nil
}

// annotateChunkLineRanges enriches byte chunks with line spans for human-driven
// workflows. Byte offsets remain authoritative; line ranges are navigation hints.
func annotateChunkLineRanges(path string, chunks []Chunk) []Chunk {
	lineStart := 1
	for i := range chunks {
		if chunks[i].ByteLength <= 0 {
			continue
		}
		lines := countLinesInRange(path, chunks[i].ByteOffset, chunks[i].ByteLength)
		chunks[i].LineStart = lineStart
		chunks[i].LineEnd = lineStart + lines - 1
		lineStart += lines
	}
	return chunks
}

func countLinesInRange(path string, offset, length int64) int {
	file, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 1
	}
	remaining := length
	buf := make([]byte, 64*1024)
	newlines := 0
	var last byte
	seen := false
	for remaining > 0 {
		toRead := int64(len(buf))
		if toRead > remaining {
			toRead = remaining
		}
		n, err := file.Read(buf[:toRead])
		if n > 0 {
			newlines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
			seen = true
			remaining -= int64(n)
		}
		if err != nil {
			break
		}
	}
	if !seen {
		return 1
	}
	if last == '\n' {
		return max(1, newlines)
	}
	return newlines + 1
}

func nextLineBoundary(file *os.File, offset, size int64) (int64, error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	buf := make([]byte, 64*1024)
	pos := offset
	limit := int64(MaxScanTokenSize)
	for pos < size && limit > 0 {
		toRead := int64(len(buf))
		if toRead > size-pos {
			toRead = size - pos
		}
		if toRead > limit {
			toRead = limit
		}
		n, err := file.Read(buf[:toRead])
		if n > 0 {
			if idx := bytes.IndexByte(buf[:n], '\n'); idx >= 0 {
				return pos + int64(idx) + 1, nil
			}
			pos += int64(n)
			limit -= int64(n)
		}
		if err == io.EOF {
			return size, nil
		}
		if err != nil {
			return 0, err
		}
	}
	return offset, nil
}

func readLines(path string, offset, limit int, lineNumbers bool) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if offset <= 0 {
		offset = 1
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxScanTokenSize)
	var lines []string
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		line := scanner.Text()
		if lineNumbers {
			line = fmt.Sprintf("%6d: %s", lineNo, line)
		}
		lines = append(lines, line)
		if limit > 0 && len(lines) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

func readByteRange(path string, offset, limit int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	var r io.Reader = file
	if limit >= 0 {
		r = io.LimitReader(file, limit)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	return string(data), int64(len(data)), nil
}

func sniffFile(path string) (bool, string) {
	file, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer file.Close()
	buf := make([]byte, 8192)
	n, _ := file.Read(buf)
	mimeType := http.DetectContentType(buf[:n])
	return bytes.Contains(buf[:n], []byte{0}), mimeType
}

func sha256Prefix(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	h := sha256.New()
	_, _ = io.CopyN(h, file, 1024*1024)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func existingFileMode(path string, fallback os.FileMode) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return fallback
	}
	return info.Mode().Perm()
}

func copyToTemp(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func persistSnapshot(path string) (string, error) {
	dir, err := snapshotDir()
	if err != nil {
		return "", err
	}
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	// Content-addressed storage dedups identical snapshots so repeated edits
	// that recreate the same content do not accumulate duplicate blobs.
	return StoreSnapshotContentAddressed(dir, in)
}

func snapshotDir() (string, error) {
	return SnapshotDir()
}

func largePlaceholder(path string, manifest Manifest) string {
	return fmt.Sprintf("[large file snapshot stored separately: path=%s size_bytes=%d sha256_prefix=%s]", path, manifest.SizeBytes, manifest.SHA256Prefix)
}
