# Local Docker Agent

Cairn includes an optional local LLM agent for Docker-focused help. It is read-only: it can inspect Cairn's Docker inventory, selected project metadata, selected Docker/Compose files, container logs, image details, and network details, but it does not execute mutations.

## Default Runtime

By default Cairn tries Ollama at:

```text
http://127.0.0.1:11434
```

On startup or refresh, Cairn calls the local model-list endpoint and selects a model:

1. Keep the configured model if it is installed.
2. Otherwise choose the first installed model from Cairn's preferred list.
3. Otherwise choose the first installed model returned by the local runtime.

The preferred order starts with `gemma4:12b`, then coder/general fallbacks such as `qwen2.5-coder`, `deepseek-coder-v2`, `llama3.1`, `mistral`, `codellama`, and `gemma3`.

## Settings

Open `Settings -> Agent` to change:

- Enabled state
- Provider: Ollama or OpenAI-compatible
- Endpoint
- Preferred model
- Maximum context lines sent to the model

The selected model is persisted after discovery, so if `gemma4:12b` is not installed and `qwen2.5-coder:7b` is available, Cairn will remember `qwen2.5-coder:7b`.

## Tool Context

The agent can include read-only context from these tools:

- Docker engine summary
- Compose projects
- Containers
- Project detail
- Project Docker/Compose/manifests
- Container detail
- Recent logs
- Network detail
- Image detail

Secrets are redacted before file and JSON context is sent to the local model. Registry credentials are never stored by the agent.

## Limits

The agent does not run Docker commands, edit files, or apply updates. When it suggests a destructive or mutating action, the user must run that action through Cairn's normal command-plan confirmation flow.
