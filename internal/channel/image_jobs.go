package channel

import (
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/imagegen"
)

// ImageJobAcknowledgement renders only stable receipt identifiers and counts.
func ImageJobAcknowledgement(receipts []imagegen.JobReceipt, rejected int) string {
	lines := []string{fmt.Sprintf("图片任务：已受理 %d 个，拒绝 %d 个。", len(receipts), rejected)}
	for _, receipt := range receipts {
		if generationID := strings.TrimSpace(receipt.GenerationID); generationID != "" {
			lines = append(lines, fmt.Sprintf("- 任务 ID：`%s`", generationID))
		}
	}
	return strings.Join(lines, "\n")
}
