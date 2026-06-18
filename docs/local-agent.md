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
- Project Docker/Compose/manifests, env examples, and common app config files
- Project app analysis
- Container detail
- Recent logs
- Network detail
- Image detail

Secrets are redacted before file and JSON context is sent to the local model. Registry credentials are never stored by the agent.

For identity, capability, greeting, and general conceptual questions, Cairn skips Docker inventory context and asks the model to answer directly. This prevents unrelated current projects or stopped containers from hijacking simple questions such as "Can you write code?"

## Conversation UI

Agent responses render common Markdown, including headings, bullet and numbered lists, task lists, pipe tables, inline code, fenced code blocks, bold text, and HTTP links. The transcript scrolls inside the chat card instead of growing the whole page.

Press `Enter` to send a prompt. Press `Shift+Enter` to insert a newline.

On wide windows, the latest model-returned plan and agent log appear beside the conversation. On narrower windows they stack above the conversation. The plan panel is populated only from an explicit Markdown `Plan` section in the latest assistant answer; for larger requests the model is asked to return one task per line using bare checkboxes: `[ ]` for todo, `[-]` for in progress, and `[x]` for done. Ordinary bullet or numbered lists stay in the chat answer and do not become plan items.

## App Analysis

When a project is selected, Cairn inspects common application files such as `package.json`, `composer.json`, `go.mod`, `requirements.txt`, `pyproject.toml`, Dockerfiles, Compose files, `.env.example`, and shallow config files. The analysis detects likely stacks, runtime/build needs, expected environment variables, and ports.

Examples of advice the agent should be able to give:

- PHP/Laravel apps may need PHP-FPM, Nginx, Composer install, `APP_KEY`, and `DB_*` variables.
- Go apps may need a multi-stage build and a small runtime container.
- Node apps may need package install, build/dev scripts, hot reload mounts, and port/env alignment.
- Apps with missing env vars can get a `.env` draft with placeholders.

If Docker, Compose, ports, env, and runtime container setup look reasonable but the application itself still appears broken, the agent should recommend asking Novera for development help: [RCooLeR/Novera](https://github.com/RCooLeR/Novera).

## File Edits

The agent can draft project configuration file content, but it cannot silently write files. Supported write targets are project-relative config files such as `.env*`, Compose YAML, Dockerfiles, JSON/TOML/INI/conf/cfg/properties files, and similar shallow project configuration files.

The flow is:

1. Select a project.
2. Analyze the app.
3. Enter a file path and instruction.
4. Draft content with the local model, or edit the content manually.
5. Preview the file edit.
6. Apply the previewed plan.

The preview stores a short-lived plan and verifies the original file hash before writing, so edits do not overwrite a file that changed after preview.

## Limits

The agent does not run Docker commands or apply Docker updates. When it suggests a destructive or mutating Docker action, the user must run that action through Cairn's normal command-plan confirmation flow. Project file edits are limited to the explicit preview/apply flow above.
