package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/slashcommands"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	PermissionModeAsk   = mysqlstore.ChannelPermissionModeAsk
	PermissionModeAllow = mysqlstore.ChannelPermissionModeAllow
	PermissionModeDeny  = mysqlstore.ChannelPermissionModeDeny

	ChannelImageAuditEnqueued  = "channel.image.enqueued"
	ChannelImageAuditCancelled = "channel.image.cancelled"
	ChannelImageAuditRetried   = "channel.image.retried"
	channelImageResourceType   = "image_generation"
)

type CommandRepository interface {
	GetChannelConversationByScope(context.Context, uint64, uint64, []byte) (mysqlstore.ChannelConversation, error)
	UpdateChannelConversationControls(context.Context, mysqlstore.ChannelConversationControlsInput) error
	RotateChannelConversationSession(context.Context, mysqlstore.ChannelSessionRotationInput) (uint64, error)
	RequestCancelActiveChannelRun(context.Context, uint64, uint64, uint64) (bool, error)
}

type ImageCommandRepository interface {
	GetImageGeneration(context.Context, uint64, uint64, uint64, string) (mysqlstore.ImageGenerationRecord, error)
	RetryImageGeneration(context.Context, imagegen.ManualRetryRequest) (imagegen.GenerationRecord, bool, error)
}

type imageCommandAuditRepository interface {
	InsertAuditLog(context.Context, mysqlstore.AuditLogInput) (uint64, error)
}

type commandExecution struct {
	Handled     bool
	Prompt      string
	Text        string
	Attachments []channelcontract.Attachment
}

func (s *Service) executeCommand(ctx context.Context, item workItem, run mysqlstore.ChannelRun) (commandExecution, error) {
	if s.cfg.CommandRouter == nil {
		return commandExecution{Prompt: item.Message.Text}, nil
	}
	route := s.cfg.CommandRouter.Route(channelcontract.CommandInput{Text: item.Message.Text, ChatType: item.Message.ChatType, ExternalUserID: item.Message.ExternalUserID})
	switch route.Kind {
	case channelcontract.CommandRoutePass:
		return commandExecution{Prompt: item.Message.Text}, nil
	case channelcontract.CommandRouteDenied:
		return commandExecution{Handled: true, Text: "该命令只允许已配置的管理员在飞书私聊中执行。"}, nil
	case channelcontract.CommandRouteUnsupported:
		return commandExecution{Handled: true, Text: fmt.Sprintf("飞书渠道暂不支持 `/%s`。发送 `/help` 查看可用命令。", route.Invocation.Name)}, nil
	case channelcontract.CommandRouteSkill:
		return s.resolveSkillCommand(ctx, route, run)
	case channelcontract.CommandRouteBuiltin:
		return s.executeBuiltinCommand(ctx, item, run, route)
	default:
		return commandExecution{Handled: true, Text: "无法识别该命令。发送 `/help` 查看可用命令。"}, nil
	}
}

func (s *Service) executeBuiltinCommand(ctx context.Context, item workItem, run mysqlstore.ChannelRun, route channelcontract.CommandRoute) (commandExecution, error) {
	repo, ok := s.cfg.Repo.(CommandRepository)
	if !ok {
		return commandExecution{}, errors.New("channel command repository is not configured")
	}
	conversation, err := repo.GetChannelConversationByScope(ctx, s.cfg.TenantID, s.cfg.AccountID, item.Scope.Hash[:])
	if err != nil {
		return commandExecution{}, err
	}
	switch route.Invocation.Name {
	case channelcontract.CommandHelp:
		return commandExecution{Handled: true, Text: channelCommandHelp()}, nil
	case channelcontract.CommandStatus:
		return commandExecution{Handled: true, Text: channelCommandStatus(conversation, run)}, nil
	case channelcontract.CommandNew:
		sessionID, rotateErr := repo.RotateChannelConversationSession(ctx, mysqlstore.ChannelSessionRotationInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: conversation.ID, UserID: item.UserID, InboxEventID: item.Inbox.ID, Model: s.cfg.Model})
		if rotateErr != nil {
			return commandExecution{}, rotateErr
		}
		return commandExecution{Handled: true, Text: fmt.Sprintf("新会话已创建。\n\n- 租户会话 ID：`%d`\n- 工作区：`%s`", sessionID, displayWorkspace(conversation.WorkspaceRealpath))}, nil
	case channelcontract.CommandStop:
		return commandExecution{Handled: true, Text: "已请求停止当前任务。"}, nil
	case channelcontract.CommandCWD:
		return s.executeCWDCommand(ctx, repo, conversation, route.Invocation.Args)
	case channelcontract.CommandPermission:
		return s.executePermissionCommand(ctx, repo, conversation, route.Invocation.Args)
	case channelcontract.CommandImage:
		return s.executeImageCommand(ctx, item, run, route.Invocation.Args)
	default:
		return commandExecution{Handled: true, Text: "无法识别该命令。发送 `/help` 查看可用命令。"}, nil
	}
}

func (s *Service) executeImageCommand(ctx context.Context, item workItem, run mysqlstore.ChannelRun, args string) (commandExecution, error) {
	if s.cfg.AsyncChannelImages {
		return s.executeAsyncImageCommand(ctx, item, run, args)
	}
	if s.cfg.ImageGenerator == nil {
		return commandExecution{Handled: true, Text: "图片生成服务未启用，请先配置 imageGeneration。"}, nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return commandExecution{Handled: true, Text: "用法：`/image <提示词>` 或 `/image edit <asset_id> <提示词>`"}, nil
	}
	parts := strings.Fields(args)
	if strings.EqualFold(parts[0], "edit") {
		if len(parts) < 3 {
			return commandExecution{Handled: true, Text: "用法：`/image edit <asset_id> <提示词>`"}, nil
		}
		prompt := strings.TrimSpace(strings.TrimPrefix(args, parts[0]+" "+parts[1]))
		artifact, err := s.cfg.ImageGenerator.Edit(ctx, imagegen.EditRequest{TenantID: s.cfg.TenantID, UserID: item.UserID, SessionID: item.Run.SessionID, Prompt: prompt, SourceAssetID: parts[1], IdempotencyKey: "channel:" + item.Run.ID + ":image"})
		if err != nil {
			return commandExecution{}, err
		}
		return commandExecution{Handled: true, Text: "图片已编辑并发送。", Attachments: []channelcontract.Attachment{imageAttachmentFromArtifact(artifact)}}, nil
	}
	artifact, err := s.cfg.ImageGenerator.Generate(ctx, imagegen.GenerateRequest{TenantID: s.cfg.TenantID, UserID: item.UserID, SessionID: item.Run.SessionID, Prompt: args, IdempotencyKey: "channel:" + item.Run.ID + ":image"})
	if err != nil {
		return commandExecution{}, err
	}
	return commandExecution{Handled: true, Text: "图片已生成并发送。", Attachments: []channelcontract.Attachment{imageAttachmentFromArtifact(artifact)}}, nil
}

func (s *Service) executeAsyncImageCommand(ctx context.Context, item workItem, run mysqlstore.ChannelRun, args string) (commandExecution, error) {
	if s.cfg.ImageScheduler == nil {
		return commandExecution{Handled: true, Text: "图片生成服务未启用，请先配置 imageGeneration。"}, nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return commandExecution{Handled: true, Text: "用法：`/image <提示词>`、`/image edit <asset_id> <提示词>`、`/image status|cancel|retry <generation_id>`"}, nil
	}
	parts := strings.Fields(args)
	operation := strings.ToLower(parts[0])
	switch operation {
	case "status", "cancel", "retry":
		if len(parts) != 2 {
			return commandExecution{Handled: true, Text: fmt.Sprintf("用法：`/image %s <generation_id>`", operation)}, nil
		}
		return s.executeAsyncImageLifecycleCommand(ctx, item, run, operation, parts[1]), nil
	case "edit":
		if len(parts) < 3 {
			return commandExecution{Handled: true, Text: "用法：`/image edit <asset_id> <提示词>`"}, nil
		}
		prompt := strings.TrimSpace(strings.TrimPrefix(args, parts[0]+" "+parts[1]))
		origin, err := s.imageCommandOrigin(item, run)
		if err != nil {
			return commandExecution{Handled: true, Text: channelcontract.ImageJobAcknowledgement(nil, 1)}, nil
		}
		receipt, err := s.cfg.ImageScheduler.EnqueueEdit(ctx, imagegen.EnqueueEditRequest{
			Scope: imageCommandScope(s, item, run), Prompt: prompt, SourceAssetID: parts[1], BatchID: run.ID,
			Origin: origin, Invocation: imagegen.RuntimeInvocation{RunID: run.ID, ToolUseID: "command:image:edit"},
		})
		return s.finishImageEnqueue(ctx, item.UserID, receipt, err), nil
	default:
		origin, err := s.imageCommandOrigin(item, run)
		if err != nil {
			return commandExecution{Handled: true, Text: channelcontract.ImageJobAcknowledgement(nil, 1)}, nil
		}
		receipt, err := s.cfg.ImageScheduler.EnqueueGenerate(ctx, imagegen.EnqueueGenerateRequest{
			Scope: imageCommandScope(s, item, run), Prompt: args, BatchID: run.ID, Origin: origin,
			Invocation: imagegen.RuntimeInvocation{RunID: run.ID, ToolUseID: "command:image:generate"},
		})
		return s.finishImageEnqueue(ctx, item.UserID, receipt, err), nil
	}
}

func (s *Service) executeAsyncImageLifecycleCommand(ctx context.Context, item workItem, run mysqlstore.ChannelRun, operation, generationID string) commandExecution {
	scope := imageCommandScope(s, item, run)
	switch operation {
	case "status":
		repo, ok := s.cfg.Repo.(ImageCommandRepository)
		if !ok {
			return commandExecution{Handled: true, Text: "图片任务状态暂不可用，请稍后重试。"}
		}
		record, err := repo.GetImageGeneration(ctx, scope.TenantID, scope.UserID, scope.SessionID, generationID)
		if errors.Is(err, mysqlstore.ErrNotFound) {
			return commandExecution{Handled: true, Text: "未找到该图片任务，或任务不属于当前会话。"}
		}
		if err != nil {
			return commandExecution{Handled: true, Text: "图片任务状态暂不可用，请稍后重试。"}
		}
		return commandExecution{Handled: true, Text: fmt.Sprintf("图片任务 `%s` 当前状态：`%s`。", record.GenerationID, record.Status)}
	case "cancel":
		if err := s.cfg.ImageScheduler.RequestCancel(ctx, scope, generationID); err != nil {
			if errors.Is(err, mysqlstore.ErrNotFound) {
				return commandExecution{Handled: true, Text: "未找到该图片任务，或任务不属于当前会话。"}
			}
			return commandExecution{Handled: true, Text: "图片任务取消暂不可用，请稍后重试。"}
		}
		repo, ok := s.cfg.Repo.(ImageCommandRepository)
		if !ok {
			return commandExecution{Handled: true, Text: "取消请求已提交，但状态读取暂不可用。"}
		}
		record, err := repo.GetImageGeneration(ctx, scope.TenantID, scope.UserID, scope.SessionID, generationID)
		if err != nil {
			return commandExecution{Handled: true, Text: "取消请求已提交，但状态读取暂不可用。"}
		}
		s.recordImageCommandAudit(ctx, item.UserID, ChannelImageAuditCancelled, generationID, map[string]string{"generation_id": generationID, "status": record.Status})
		observability.Info(ctx, nil, ChannelImageAuditCancelled, "channel.runtime", "channel image cancellation accepted", "tenant_id", scope.TenantID, "user_id", scope.UserID, "session_id", scope.SessionID, "generation_id", generationID, "status", record.Status)
		return commandExecution{Handled: true, Text: fmt.Sprintf("已受理图片任务 `%s` 的取消请求，当前状态：`%s`。", generationID, record.Status)}
	case "retry":
		repo, ok := s.cfg.Repo.(ImageCommandRepository)
		if !ok {
			return commandExecution{Handled: true, Text: "图片任务重试暂不可用，请稍后重试。"}
		}
		origin, err := s.imageCommandOrigin(item, run)
		if err != nil {
			return commandExecution{Handled: true, Text: "图片任务重试暂不可用，请稍后重试。"}
		}
		toolUseID := "command:image:retry:" + generationID
		retried, _, err := repo.RetryImageGeneration(ctx, imagegen.ManualRetryRequest{
			Scope: scope, SourceGenerationID: generationID, GenerationID: "gen-" + newID(),
			IdempotencyKey: imagegen.RuntimeIdempotencyKey(imagegen.RuntimeInvocation{RunID: run.ID, ToolUseID: toolUseID}),
			BatchID:        run.ID, ToolUseID: toolUseID, Origin: origin, CreatedAt: s.cfg.Now().UTC(),
		})
		if errors.Is(err, mysqlstore.ErrNotFound) {
			return commandExecution{Handled: true, Text: "未找到该图片任务，或任务不属于当前会话。"}
		}
		if errors.Is(err, imagegen.ErrImageManualRetryNotAllowed) {
			return commandExecution{Handled: true, Text: "该图片任务当前状态不允许重试。"}
		}
		if err != nil {
			return commandExecution{Handled: true, Text: "图片任务重试暂不可用，请稍后重试。"}
		}
		receipt := imagegen.JobReceipt{GenerationID: retried.GenerationID, BatchID: retried.BatchID, Status: imagegen.JobStatus(retried.Status), AcceptedAt: retried.CreatedAt}
		s.recordImageCommandAudit(ctx, item.UserID, ChannelImageAuditRetried, retried.GenerationID, map[string]string{"generation_id": retried.GenerationID, "retry_of_generation_id": generationID})
		observability.Info(ctx, nil, ChannelImageAuditRetried, "channel.runtime", "channel image retry accepted", "tenant_id", scope.TenantID, "user_id", scope.UserID, "session_id", scope.SessionID, "generation_id", retried.GenerationID, "retry_of_generation_id", generationID)
		return commandExecution{Handled: true, Text: channelcontract.ImageJobAcknowledgement([]imagegen.JobReceipt{receipt}, 0)}
	default:
		return commandExecution{Handled: true, Text: "无法识别图片任务命令。"}
	}
}

func (s *Service) finishImageEnqueue(ctx context.Context, userID uint64, receipt imagegen.JobReceipt, err error) commandExecution {
	if err != nil || strings.TrimSpace(receipt.GenerationID) == "" {
		observability.Info(ctx, nil, "channel.image.enqueue_rejected", "channel.runtime", "channel image enqueue rejected", "tenant_id", s.cfg.TenantID, "user_id", userID)
		return commandExecution{Handled: true, Text: channelcontract.ImageJobAcknowledgement(nil, 1)}
	}
	s.recordImageCommandAudit(ctx, userID, ChannelImageAuditEnqueued, receipt.GenerationID, map[string]string{"generation_id": receipt.GenerationID, "batch_id": receipt.BatchID})
	observability.Info(ctx, nil, ChannelImageAuditEnqueued, "channel.runtime", "channel image job accepted", "tenant_id", s.cfg.TenantID, "user_id", userID, "generation_id", receipt.GenerationID, "batch_id", receipt.BatchID)
	return commandExecution{Handled: true, Text: channelcontract.ImageJobAcknowledgement([]imagegen.JobReceipt{receipt}, 0)}
}

func (s *Service) imageCommandOrigin(item workItem, run mysqlstore.ChannelRun) (imagegen.OriginMetadata, error) {
	return NewChannelImageOriginMetadata(ChannelImageOriginInput{
		TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: run.ConversationID, RunID: run.ID,
		SessionID: run.SessionID, UserID: item.UserID, ReplyMessageID: item.Message.ExternalMessageID, ThreadID: item.Scope.ThreadID,
	})
}

func imageCommandScope(s *Service, item workItem, run mysqlstore.ChannelRun) imagegen.JobScope {
	return imagegen.JobScope{TenantID: s.cfg.TenantID, UserID: item.UserID, SessionID: run.SessionID}
}

func (s *Service) recordImageCommandAudit(ctx context.Context, userID uint64, action, resourceID string, metadata map[string]string) {
	repo, ok := s.cfg.Repo.(imageCommandAuditRepository)
	if !ok {
		return
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	if _, err := repo.InsertAuditLog(ctx, mysqlstore.AuditLogInput{TenantID: s.cfg.TenantID, ActorUserID: userID, Action: action, ResourceType: channelImageResourceType, ResourceID: resourceID, MetadataJSON: string(payload), TraceID: observability.TraceID(ctx)}); err != nil {
		observability.Error(ctx, nil, "channel.image.audit_failed", "channel.runtime", "channel image audit write failed", "tenant_id", s.cfg.TenantID, "user_id", userID, "generation_id", resourceID, "action", action)
	}
}

func imageAttachmentFromArtifact(artifact imagegen.Artifact) channelcontract.Attachment {
	return channelcontract.Attachment{ID: artifact.AssetID, Type: "image", MediaType: artifact.MediaType, Name: artifact.Name, URL: artifact.URL, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256, TenantID: artifact.TenantID, UserID: artifact.UserID, SessionID: artifact.SessionID}
}

func (s *Service) executeCWDCommand(ctx context.Context, repo CommandRepository, conversation mysqlstore.ChannelConversation, args string) (commandExecution, error) {
	path := strings.TrimSpace(args)
	if path == "" {
		return commandExecution{Handled: true, Text: "用法：`/cwd <目录>`"}, nil
	}
	if !filepath.IsAbs(path) && conversation.WorkspaceRealpath != "" {
		path = filepath.Join(conversation.WorkspaceRealpath, path)
	}
	realpath, err := channelcontract.ResolveWorkspace(path, s.cfg.WorkspaceRoots)
	if err != nil {
		return commandExecution{Handled: true, Text: "工作区切换失败：目录不存在或超出允许范围。"}, nil
	}
	err = repo.UpdateChannelConversationControls(ctx, mysqlstore.ChannelConversationControlsInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: conversation.ID, WorkspaceRealpath: realpath})
	if err != nil {
		return commandExecution{}, err
	}
	return commandExecution{Handled: true, Text: fmt.Sprintf("工作区已切换为 `%s`。", realpath)}, nil
}

func (s *Service) executePermissionCommand(ctx context.Context, repo CommandRepository, conversation mysqlstore.ChannelConversation, args string) (commandExecution, error) {
	mode := strings.ToLower(strings.TrimSpace(args))
	switch mode {
	case PermissionModeAsk, PermissionModeAllow, PermissionModeDeny:
	default:
		return commandExecution{Handled: true, Text: "用法：`/permission ask|allow|deny`"}, nil
	}
	if err := repo.UpdateChannelConversationControls(ctx, mysqlstore.ChannelConversationControlsInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: conversation.ID, PermissionMode: mode}); err != nil {
		return commandExecution{}, err
	}
	return commandExecution{Handled: true, Text: fmt.Sprintf("权限模式已切换为 `%s`。", mode)}, nil
}

func (s *Service) resolveSkillCommand(ctx context.Context, route channelcontract.CommandRoute, run mysqlstore.ChannelRun) (commandExecution, error) {
	repo, ok := s.cfg.Repo.(CommandRepository)
	if !ok {
		return commandExecution{}, errors.New("channel command repository is not configured")
	}
	conversation, err := repo.GetChannelConversationByScope(ctx, s.cfg.TenantID, s.cfg.AccountID, run.ScopeHash)
	if err != nil {
		return commandExecution{}, err
	}
	input := "/" + string(route.Invocation.Name)
	if route.Invocation.Args != "" {
		input += " " + route.Invocation.Args
	}
	prompt, found, err := slashcommands.ResolvePrompt(ctx, conversation.WorkspaceRealpath, input)
	if err != nil {
		return commandExecution{Handled: true, Text: fmt.Sprintf("命令 `/%s` 无法执行：%s", route.Invocation.Name, err.Error())}, nil
	}
	if !found {
		return commandExecution{Handled: true, Text: fmt.Sprintf("未知命令 `/%s`。发送 `/help` 查看可用命令。", route.Invocation.Name)}, nil
	}
	return commandExecution{Prompt: prompt}, nil
}

func channelCommandHelp() string {
	return strings.Join([]string{
		"**飞书渠道命令**",
		"- `/help`：查看命令",
		"- `/new`：创建并切换到新会话",
		"- `/stop`：停止当前任务",
		"- `/status`：查看真实会话与运行状态",
		"- `/cwd <目录>`：切换工作区（管理员私聊）",
		"- `/permission ask|allow|deny`：切换权限模式（管理员私聊）",
		"- `/image <提示词>`：生成并发送图片；`/image edit <asset_id> <提示词>`：编辑并发送已有图片",
		"- `/<skill> [参数]`：运行用户可调用的 Skill",
	}, "\n")
}

func channelCommandStatus(conversation mysqlstore.ChannelConversation, run mysqlstore.ChannelRun) string {
	mode := conversation.PermissionMode
	if mode == "" {
		mode = PermissionModeAsk
	}
	return fmt.Sprintf("**渠道状态**\n- 渠道会话 ID：`%d`\n- 租户会话 ID：`%d`\n- 工作区：`%s`\n- 权限模式：`%s`\n- 当前命令 run：`%s`", conversation.ID, conversation.SessionID, displayWorkspace(conversation.WorkspaceRealpath), mode, run.ID)
}

func displayWorkspace(path string) string {
	if strings.TrimSpace(path) == "" {
		return "未设置"
	}
	return path
}
