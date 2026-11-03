// attachments.go 管理附件托盘与剪贴板图片导入。

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func firstClipboardImporter(importer ClipboardImageImporter) ClipboardImageImporter {
	if importer != nil {
		return importer
	}
	return DefaultClipboardImageImporter
}

func firstClipboardDetector(detector ClipboardImageDetector) ClipboardImageDetector {
	if detector != nil {
		return detector
	}
	return DefaultClipboardImageDetector
}

func (m Model) attachmentTrayView() string {
	if len(m.attachments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(statusStyle.Render("Attachments  ctrl+d remove last  ctrl+u clear all"))
	for index, attachment := range m.attachments {
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(strconv.Itoa(index + 1))
		b.WriteString(". ")
		b.WriteString(attachmentLabel(attachment))
	}
	return b.String()
}

func attachmentLabel(attachment Attachment) string {
	parts := []string{attachmentIcon(attachment), firstNonEmpty(attachment.Type, "file")}
	if attachment.Name != "" {
		parts = append(parts, attachment.Name)
	}
	if attachment.MediaType != "" {
		parts = append(parts, attachment.MediaType)
	}
	if attachment.SizeBytes > 0 {
		parts = append(parts, humanBytes(attachment.SizeBytes))
	}
	target := firstNonEmpty(attachment.URL, attachment.Path)
	if target != "" {
		parts = append(parts, target)
	}
	return strings.Join(parts, " · ")
}

func attachmentIcon(attachment Attachment) string {
	mediaType := strings.ToLower(strings.TrimSpace(attachment.MediaType))
	kind := strings.ToLower(strings.TrimSpace(attachment.Type))
	name := strings.ToLower(strings.TrimSpace(attachment.Name))
	switch {
	case kind == "image" || strings.HasPrefix(mediaType, "image/") || hasAnySuffix(name, ".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic"):
		return "[img]"
	case kind == "video" || strings.HasPrefix(mediaType, "video/") || hasAnySuffix(name, ".mp4", ".mov", ".webm", ".mkv"):
		return "[video]"
	case kind == "audio" || strings.HasPrefix(mediaType, "audio/") || hasAnySuffix(name, ".mp3", ".wav", ".m4a", ".flac", ".ogg"):
		return "[audio]"
	case strings.Contains(mediaType, "pdf") || hasAnySuffix(name, ".pdf"):
		return "[pdf]"
	case strings.Contains(mediaType, "json") || hasAnySuffix(name, ".json", ".yaml", ".yml", ".toml"):
		return "[data]"
	case strings.HasPrefix(mediaType, "text/") || hasAnySuffix(name, ".txt", ".md", ".go", ".js", ".ts", ".tsx", ".py", ".java", ".rs", ".rb", ".php", ".css", ".html"):
		return "[text]"
	default:
		return "[file]"
	}
}

func hasAnySuffix(value string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func attachmentMeta(attachments []Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	refs := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.ID > 0 {
			refs = append(refs, formatImageRef(attachment.ID))
		}
	}
	if len(refs) > 0 {
		return fmt.Sprintf("%d image(s): %s", len(refs), strings.Join(refs, ", "))
	}
	return fmt.Sprintf("%d attachment(s)", len(attachments))
}

func promptWithAttachments(prompt string, attachments []Attachment) string {
	prompt = strings.TrimSpace(prompt)
	if len(attachments) == 0 {
		return prompt
	}
	var b strings.Builder
	if prompt != "" {
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	b.WriteString("Attached context:\n")
	for _, attachment := range attachments {
		b.WriteString("- ")
		b.WriteString(attachmentLabel(attachment))
		b.WriteString("\n")
	}
	return b.String()
}

func formatImageRef(id int) string {
	if id <= 0 {
		return "[Image]"
	}
	return "[Image #" + strconv.Itoa(id) + "]"
}

func referencedAttachments(prompt string, attachments []Attachment) []Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.ID <= 0 || strings.Contains(prompt, formatImageRef(attachment.ID)) {
			out = append(out, attachment)
		}
	}
	return out
}

func (m *Model) removeLastAttachment() bool {
	if len(m.attachments) == 0 {
		return false
	}
	attachment := m.attachments[len(m.attachments)-1]
	m.attachments = m.attachments[:len(m.attachments)-1]
	if attachment.ID > 0 {
		m.removeTextFromInput(formatImageRef(attachment.ID))
	}
	m.err = nil
	return true
}

func (m *Model) removeTextFromInput(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	value := m.textarea.Value()
	value = strings.ReplaceAll(value, text, "")
	value = strings.Join(strings.Fields(value), " ")
	m.setTextareaValue(value)
	m.updateSlashSuggestions()
}

func (m *Model) insertTextAtCursor(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	value := m.textarea.Value()
	if strings.TrimSpace(value) == "" {
		m.setTextareaValue(text)
		return
	}
	separator := " "
	if strings.HasSuffix(value, " ") || strings.HasSuffix(value, "\n") {
		separator = ""
	}
	m.setTextareaValue(value + separator + text)
}

func humanBytes(size int64) string {
	if size < 1024 {
		return strconv.FormatInt(size, 10) + "B"
	}
	units := []string{"KB", "MB", "GB"}
	value := float64(size)
	for _, unit := range units {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f%s", value, unit)
		}
	}
	return fmt.Sprintf("%.1fTB", value/1024)
}

func DefaultClipboardImageImporter(ctx context.Context) (Attachment, bool, error) {
	if runtime.GOOS != "darwin" {
		return Attachment{}, false, nil
	}
	dir := filepath.Join(os.TempDir(), "niuma-attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Attachment{}, false, err
	}
	path := filepath.Join(dir, "clipboard-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".png")
	if err := runPNGClipboardImport(ctx, path); err != nil {
		if errors.Is(err, errNoClipboardImage) {
			return Attachment{}, false, nil
		}
		return Attachment{}, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, false, err
	}
	if info.Size() == 0 {
		_ = os.Remove(path)
		return Attachment{}, false, nil
	}
	return Attachment{Type: "image", MediaType: "image/png", Name: filepath.Base(path), Path: path, SizeBytes: info.Size()}, true, nil
}

var errNoClipboardImage = errors.New("clipboard does not contain an image")

func DefaultClipboardImageDetector(ctx context.Context) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "osascript", "-e", "the clipboard as «class PNGf»")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

func (m Model) checkClipboardImage() tea.Cmd {
	if m.detectClipboardImage == nil || m.busy {
		return nil
	}
	return func() tea.Msg {
		ok, err := m.detectClipboardImage(m.ctx)
		return clipboardImageHintMsg{ok: ok, err: err}
	}
}

func runPNGClipboardImport(ctx context.Context, path string) error {
	if _, err := exec.LookPath("pngpaste"); err == nil {
		cmd := exec.CommandContext(ctx, "pngpaste", path)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(strings.ToLower(string(output)), "no image") {
				return errNoClipboardImage
			}
			return fmt.Errorf("pngpaste clipboard image: %w", err)
		}
		return nil
	}
	script := `set outPath to POSIX file "` + strings.ReplaceAll(path, `"`, `\"`) + `"
try
  set theImage to the clipboard as «class PNGf»
  set f to open for access outPath with write permission
  set eof of f to 0
  write theImage to f
  close access f
on error
  try
    close access outPath
  end try
  error number -128
end try`
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return errNoClipboardImage
	}
	return nil
}
