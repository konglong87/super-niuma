# Niuma Configuration

Niuma keeps its global settings in:

```text
~/.niuma/settings.json
```

The Desktop app, WebUI 2.0, CLI, TUI and background workers use the same settings model. A minimal OpenAI-compatible configuration looks like this:

```json
{
  "provider": "openai-compatible",
  "providerProtocol": "openai-chat-completions",
  "baseURL": "https://model.example.com/v1",
  "apiKey": "replace-me",
  "model": "model-id"
}
```

Replace the endpoint, credential and model with values from your provider. Niuma does not ship with a default model or API key.

## Configuration guides

- [Settings quickstart](examples/settings_quickstart.md) — fields, Responses API, fallback providers, model tiers and permissions.
- [Example settings file](examples/settings.example.json) — copy this as a starting point, then remove placeholder values.
- [Getting started](getting-started.md) — install Niuma and complete the first end-to-end task.
- [Security and permissions](../SECURITY.md) — review before enabling background or remote access.

## Operational boundaries

- Niuma does not automatically scan project files for model credentials.
- Explicit `--model`, `--provider` and `--settings` values override settings for one run and do not silently rewrite the global file.
- Keep `settings.json` out of Git and restrict its permissions on Unix-like systems:

```bash
chmod 600 ~/.niuma/settings.json
niuma doctor
```

“Always on” means background tasks and scheduling are available. It does not mean unlimited usage, guaranteed completion or operation without device, provider, budget and permission limits.
