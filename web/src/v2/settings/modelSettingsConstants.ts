export const PRIMARY_PROVIDER = -1;
export const PROTOCOL_RESPONSES = "openai-responses";
export const PROVIDER_TYPES = [["", "继承默认"], ["custom", "Custom API"], ["openai", "OpenAI"], ["openai-compatible", "OpenAI Compatible"]] as const;
export const PROTOCOLS = [["", "自动选择"], ["anthropic-messages", "Messages"], ["openai-chat-completions", "OpenAI Chat Completions"], [PROTOCOL_RESPONSES, "OpenAI Responses"]] as const;
export const BOOL_OPTIONS = [["", "继承默认"], ["true", "启用"], ["false", "关闭"]] as const;
export const IMAGE_ROOT = ["imageGeneration"] as const;
export const FALLBACK_PROVIDERS = ["fallback", "providers"] as const;
export const COMPUTER_USE_ROUTES = ["computerUse", "imageInputRoutes"] as const;
