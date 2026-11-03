// nextsteps.go 为「turn 结束后给用户的下一步提示词候选」构造请求内容。
// 提示词与解析逻辑在这里单点实现，TUI 与 Web 两端共用，只在传输方式上分叉。

package nextsteps

import (
	"fmt"
	"strings"
)

const (
	// DefaultCount 是默认候选条数。3 条是刻意保守值：更多会明显挤压 TUI 底部空间。
	DefaultCount = 3
	// MaxCount 是候选条数上限，与数字快选键 1-5 的容量匹配。
	MaxCount = 5
	// DefaultMaxTokens 足够覆盖 MaxCount 条短提示词，同时把模型跑偏写长文的
	// 损失卡在可忽略的量级。
	DefaultMaxTokens = 256
	// MaxSuggestionRunes 之外的行视为模型在解释而非给候选，按 rune 计数以免
	// 误杀中文。
	MaxSuggestionRunes = 120
)

// Input 是构造请求所需的对话尾部。只取尾部而非整个 transcript：既控制成本，
// 也避免小模型被长上下文带偏。
type Input struct {
	UserPrompt        string
	AssistantResponse string
	ToolNames         []string
}

// BuildPrompt 构造请求内容。没有 assistant 回复时返回空串，调用方据此跳过
// 整次请求。
func BuildPrompt(in Input, count int) string {
	response := strings.TrimSpace(in.AssistantResponse)
	if response == "" {
		return ""
	}
	if count <= 0 {
		count = DefaultCount
	}
	var b strings.Builder
	fmt.Fprintf(&b, "下面是一段人机协作的编程对话的最后一轮。请为用户推荐 %d 条最有价值的下一步动作。\n\n", count)
	if prompt := strings.TrimSpace(in.UserPrompt); prompt != "" {
		b.WriteString("用户上一条请求：\n")
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	b.WriteString("助手的回复：\n")
	b.WriteString(response)
	b.WriteString("\n\n")
	if tools := dedupeToolNames(in.ToolNames); len(tools) > 0 {
		b.WriteString("本轮用到的工具：")
		b.WriteString(strings.Join(tools, "、"))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, `输出要求（严格遵守）：
- 每行一条，共 %d 条，不要编号、不要 bullet、不要 markdown、不要任何解释
- 每条都是用户接下来可以直接发给助手的一句话指令，用第二人称祈使句
- 每条不超过 30 个字，具体到这次对话的内容，不要写「继续」「还有什么」这类空话
- 只输出这 %d 行，不要有任何其它内容`, count, count)
	return b.String()
}

func dedupeToolNames(names []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}
