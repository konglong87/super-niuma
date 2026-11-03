import { useI18n } from "../../lib/i18n";

const english: Record<string, string> = {
  "继承默认": "Inherit default", "自动选择": "Automatic", "启用": "Enabled", "关闭": "Disabled",
  "隐藏密钥": "Hide credential", "显示密钥": "Show credential", "已存储，未修改时保留": "Stored; unchanged values are retained",
  "全局主模型": "Primary model", "未命名供应商": "Unnamed provider", "默认路由": "Default route", "设为主模型": "Set as primary model", "已设为主模型": "Primary model", "删除供应商": "Remove provider",
  "供应商名称": "Provider name", "供应商类型": "Provider type", "API 协议": "API protocol", "API 地址": "API base URL",
  "默认模型": "Default model", "模型标识": "Model ID", "思考强度": "Reasoning effort", "状态模式": "State mode",
  "服务端存储": "Server storage", "移除 Responses 配置": "Remove Responses configuration",
  "图片接口配置": "Image API configuration", "图片 API 协议": "Image API protocol", "继承供应商默认": "Inherit provider default",
  "图片结果域名白名单": "Allowed image result hosts",
  "请先填写供应商名称": "Enter a provider name first", "尚未测试模型目录连接": "Model catalog connection not tested",
  "测试中…": "Testing…", "测试连接": "Test connection", "供应商列表": "Providers", "供应商": "Provider",
  "继承默认模型": "Inherit default model", "添加供应商": "Add provider", "备用路由": "Fallback routing", "启用 Fallback": "Enable fallback",
  "普通模型 Fallback 链": "Standard model fallback chain", "启用普通 Fallback": "Enable standard fallback",
  "用于文本对话、代码任务和常规工具调用。这里的备用 Provider 不代表一定支持桌面截图。": "For text chat, coding tasks and standard tool calls. These fallback providers are not automatically allowed to receive desktop screenshots.",
  "普通请求和 Computer Use 分开配置": "Standard requests and Computer Use are configured separately",
  "先在上方添加备用 Provider，再在下面的 Computer Use 视觉路由中选择可接收桌面截图的模型。API 地址和密钥只需填写一次。": "Add fallback providers above, then select the models allowed to receive desktop screenshots in the Computer Use visual routes below. API URLs and credentials are entered only once.",
  "Computer Use 桌面操作": "Computer Use desktop operations",
  "Computer Use 会读取桌面截图并返回点击、输入、滚动等操作。只有支持图片输入和 ComputerUse 动作调用的模型，才应加入这条视觉路由。普通 Fallback 不会自动接收桌面截图。": "Computer Use reads desktop screenshots and returns clicks, typing and scrolling actions. Only models that support image input and ComputerUse actions should be added to this visual route. Standard fallbacks do not automatically receive desktop screenshots.",
  "这是独立的桌面视觉路由": "This is a separate desktop vision route",
  "普通模型 Fallback 用于文本和常规工具调用；Computer Use 只会把截图发送给下面明确选择的 Provider/model。": "Standard model fallbacks handle text and normal tool calls; Computer Use sends screenshots only to the providers/models explicitly selected below.",
  "请先在上方配置主模型或备用模型。": "Configure a primary or fallback model above first.",
  "Computer Use 视觉路由": "Computer Use visual routes",
  "主模型": "Primary model", "未配置模型": "Model not configured", "继承主模型": "Inherits primary model",
  "已启用截图": "Screenshots enabled", "需要模型": "Model required", "未启用截图": "Screenshots not enabled",
  "已启用": "Enabled", "个 Computer Use 视觉路由。保存后，运行中的服务可能需要重启。": "Computer Use visual routes. Running services may need a restart after saving.",
  "图片生成": "Image generation", "启用图片生成": "Enable image generation", "模型上下文包含图片预览": "Image previews in model context",
  "默认图片供应商": "Default image provider", "默认图片模型": "Default image model", "默认尺寸": "Default image size", "图片质量": "Image quality",
  "兼容配置与生成限制": "Legacy configuration and generation limits", "兼容图片供应商": "Legacy image provider", "兼容图片模型": "Legacy image model",
  "输出格式": "Output format", "图片背景": "Image background", "透明": "Transparent", "不透明": "Opaque",
  "最大图片数量": "Maximum image count", "请求超时（秒）": "Request timeout (seconds)", "最大并发": "Maximum concurrency",
  "保留天数": "Retention days", "提示词字符上限": "Maximum prompt characters", "输入大小上限（字节）": "Maximum input size (bytes)",
  "渠道异步图片生成": "Asynchronous channel image generation", "异步生成渠道账号范围": "Asynchronous channel account scope",
  "图片任务 Worker": "Image task worker", "轮询间隔（毫秒）": "Polling interval (milliseconds)", "Worker 最大并发": "Worker maximum concurrency",
  "最大尝试次数": "Maximum attempts", "心跳间隔（秒）": "Heartbeat interval (seconds)", "租约时长（秒）": "Lease duration (seconds)",
  "完成阶段超时（秒）": "Finalization timeout (seconds)", "每租户排队上限": "Queue limit per tenant", "每用户排队上限": "Queue limit per user",
  "删除这个供应商？引用它的图片或路由配置也需要调整。": "Remove this provider? Any image or routing configuration that references it will also need updating.",
  "放弃未保存的修改，恢复到上次读取的配置？": "Discard unsaved changes and restore the last loaded configuration?",
  "重新载入会放弃当前草稿，确定继续？": "Reloading will discard the current draft. Continue?",
  "正在读取全局设置…": "Loading global settings…", "重新载入": "Reload", "重试读取": "Retry loading",
  "请在全局 Settings JSON 中修正后继续。": "Correct the global Settings JSON to continue.",
  "格式化 JSON": "Format JSON", "全局 Settings JSON": "Global Settings JSON", "行": "lines", "配置校验通过": "Configuration validation passed",
  "已保存并读取确认。运行中的服务需重启以加载新配置。": "Saved and verified by readback. Restart running services to load the new configuration.",
  "有未保存的修改": "Unsaved changes", "与上次读取的配置一致": "Matches the last loaded configuration", "重置草稿": "Reset draft",
  "重置": "Reset", "校验中…": "Validating…", "校验": "Validate", "配置校验未通过": "Configuration validation failed", "保存中…": "Saving…", "保存更改": "Save changes",
  "配置根节点必须是 JSON 对象。": "The configuration root must be a JSON object.",
  "服务端配置已变更。当前草稿已保留，请重新载入最新配置后再修改。": "The server configuration has changed. Your draft is retained; reload the latest configuration before editing again.",
  "无权访问全局设置，请检查连接身份和管理权限。": "Access to global settings was denied. Check your connection identity and administrative permissions.",
  "无法连接配置服务，请检查网络后重试。": "Cannot reach the settings service. Check the connection and try again.",
  "保存后配置再次发生变化，当前草稿已保留，请重新载入确认。": "The configuration changed again after saving. Your draft is retained; reload to review the latest version.",
  "保存请求已提交，但读取确认失败。当前草稿已保留，请重新载入确认。": "The save request was submitted, but readback failed. Your draft is retained; reload to confirm the result.",
  "模型目录连接成功": "Model catalog connection succeeded", "模型目录连接失败": "Model catalog connection failed",
};

export function globalSettingsText(language: string, text: string): string {
  if (language !== "en") return text;
  if (english[text]) return english[text];
  const http = /^配置请求失败（HTTP (\d+)），请重试。$/.exec(text);
  if (http) return `Settings request failed (HTTP ${http[1]}). Please retry.`;
  if (text.startsWith("JSON 语法错误：")) {
    const line = /第 (\d+) 行，第 (\d+) 列/.exec(text);
    const position = /字符 (\d+)/.exec(text);
    return line ? `JSON syntax error at line ${line[1]}, column ${line[2]}.` : position ? `JSON syntax error at character ${position[1]}.` : "JSON syntax error. Check quotes, commas and brackets.";
  }
  return text;
}

export function useGlobalSettingsText() {
  const { language } = useI18n();
  return (text: string) => globalSettingsText(language, text);
}
