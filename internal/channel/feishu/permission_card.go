package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

func RenderPermissionCard(request channelcontract.PermissionRequest, token string) (json.RawMessage, error) {
	if strings.TrimSpace(request.ToolName) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("feishu permission card requires tool name and token")
	}
	reason := "该工具需要你的授权后才能执行。"
	card := map[string]interface{}{
		"schema": cardSchema,
		"header": map[string]interface{}{
			"template": "orange",
			"title":    map[string]string{"tag": "plain_text", "content": "需要授权"},
		},
		"body": map[string]interface{}{
			"elements": []interface{}{
				map[string]interface{}{"tag": cardTagMarkdown, "content": fmt.Sprintf("工具：`%s`\n\n%s", request.ToolName, reason)},
				permissionButton("允许一次", "primary", token, channelcontract.PermissionActionApproveOnce),
				permissionButton("本会话允许", "default", token, channelcontract.PermissionActionApproveSession),
				permissionButton("拒绝", "danger", token, channelcontract.PermissionActionDeny),
			},
		},
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("feishu permission card: marshal: %w", err)
	}
	if len(encoded) > maxCardBytes {
		return nil, errors.New("feishu permission card exceeds provider limit")
	}
	return encoded, nil
}

func permissionButton(text, buttonType, token, action string) map[string]interface{} {
	return map[string]interface{}{
		"tag":  cardTagButton,
		"text": map[string]string{"tag": "plain_text", "content": text},
		"type": buttonType,
		"behaviors": []interface{}{
			map[string]interface{}{"type": cardActionCallback, "value": map[string]string{"token": token, "action": action}},
		},
	}
}

func (a *Adapter) SendPermissionCard(ctx context.Context, request channelcontract.PermissionRequest, token string) (string, error) {
	if a == nil || a.channel == nil {
		return "", errAdapterNotInitialized
	}
	if request.ExternalChatID == "" {
		return "", errors.New("feishu permission card requires external chat id")
	}
	card, err := RenderPermissionCard(request, token)
	if err != nil {
		return "", err
	}
	result, err := a.channel.Send(ctx, &larktypes.SendInput{ChatID: request.ExternalChatID, ReplyMessageID: request.ReplyToMessageID, Card: string(card), MsgType: "interactive"})
	if err != nil || result == nil || result.MessageID == "" {
		return "", errors.New("feishu permission card delivery failed")
	}
	return result.MessageID, nil
}
