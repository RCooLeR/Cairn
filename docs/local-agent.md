# Local Docker Agent

Cairn includes an optional local LLM agent for Docker-focused help. It can inspect Cairn's Docker inventory, selected project metadata, selected Docker/Compose files, container logs, image details, and network details. It can also request approval-gated Cairn tools for Docker actions and update workflows. Project-file draft, preview, and apply tools are currently quarantined and unavailable.

## Default Runtime

By default Cairn tries Ollama at:

```text
http://127.0.0.1:11434
```

Cairn intentionally accepts only literal IPv4 or IPv6 loopback HTTP(S) endpoints with an explicit port, such as `http://127.0.0.1:11434` or `http://[::1]:11434`. It rejects hostnames (including `localhost`), remote/private/link-local and metadata addresses, URL credentials, queries, fragments, path-prefixed base URLs, proxy routing, and all redirects. This sharply reduces SSRF and accidental cross-origin disclosure, but the configured loopback service remains trusted: a malicious or misconfigured local service could still store or relay the context it receives.

On startup or refresh, Cairn calls the local model-list endpoint and selects a model:

1. Keep the configured model if it is installed.
2. Otherwise choose the first installed model from Cairn's preferred list.
3. Otherwise choose the first installed model returned by the local runtime.

The preferred order starts with `gemma4:12b-it-q8_0`, then `gemma4:12b`, then other chat/code-capable fallbacks such as `gemma4:26b`, `devstral-small-2:24b`, `gpt-oss:20b`, `granite4.1:8b`, `qwen2.5-coder`, `deepseek-coder-v2`, `llama3.1`, `mistral`, `codellama`, and `gemma3`.

## Settings

Open `Settings -> Agent` to change:

- Enabled state
- Provider: Ollama or OpenAI-compatible
- Loopback-only endpoint (literal IP plus explicit port)
- Preferred model
- Maximum context lines sent to the model

The selected model is persisted after discovery, so if `gemma4:12b-it-q8_0` is not installed but `gemma4:12b` is available, Cairn will remember `gemma4:12b`. If neither Gemma 4 preference is installed and `qwen2.5-coder:7b` is available, Cairn will remember `qwen2.5-coder:7b`.

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

Project-file previews hide scalar values. Other Docker, log, and JSON context receives bounded best-effort secret redaction, which cannot guarantee detection of every opaque value; review the selected tools before sending. Registry credentials are never stored by the agent.

For identity, capability, greeting, and general conceptual questions, Cairn skips Docker inventory context and asks the model to answer directly. This prevents unrelated current projects or stopped containers from hijacking simple questions such as "Can you write code?"

## Conversation UI

Agent responses render common Markdown, including headings, bullet and numbered lists, task lists, pipe tables, inline code, fenced code blocks, bold text, and HTTP links. The transcript scrolls inside the chat card instead of growing the whole page.

Press `Enter` to send a prompt. Press `Shift+Enter` to insert a newline.

On wide windows, the latest model-returned plan and agent log appear beside the conversation. On narrower windows they stack above the conversation. The plan panel is populated only from an explicit Markdown `Plan` section in the latest assistant answer; for larger requests the model is asked to return one task per line using bare checkboxes: `[ ]` for todo, `[-]` for in progress, and `[x]` for done. When a plan is promoted into the plan panel, that plan block is removed from the chat answer so it is not duplicated. Ordinary bullet or numbered lists stay in the chat answer and do not become plan items.

The log panel is an activity trail for the latest run. It records steps such as understanding the request, creating a plan or direct answer shape, using context tools, and providing the final model answer. It does not display raw tool summaries as the primary log content.

## Cairn Tools

The Agent can request Cairn tools for Docker/app work. The model must ask for one tool at a time using a structured `cairn-tool` JSON block. Cairn then shows an allow/decline dialog with the exact tool, reason, and arguments before execution.

The toolset covers:

- Docker inventory and inspect data for engine, projects, containers, images, volumes, networks, logs, and container files.
- Update workflows such as check all updates, check one project, create project/service update plans, and apply approved update plans.
- Compose project actions such as start, stop, restart, pull, redeploy plan, and down plan.
- Container actions such as start, stop, restart, kill plan, and remove plan.
- Image, volume, network, and prune planning/creation workflows, plus approved plan application where Cairn supports it.
- Manual project-configuration guidance for `.env`, Compose YAML, Dockerfiles, and shallow config files. The Agent cannot draft, preview, or apply those edits through Cairn while the file-edit feature is quarantined.

Approved tools execute through Cairn services, not raw model text. Destructive or dangerous work still goes through Cairn's command-plan preview, typed confirmation where required, audit trail, and progress flow.

## App Analysis

When a project is selected, Cairn inspects common application files such as `package.json`, `composer.json`, `go.mod`, `requirements.txt`, `pyproject.toml`, Dockerfiles, Compose files, `.env.example`, and shallow config files. The analysis detects likely stacks, runtime/build needs, expected environment variables, and ports.

Examples of advice the agent should be able to give:

- PHP/Laravel apps may need PHP-FPM, Nginx, Composer install, `APP_KEY`, and `DB_*` variables.
- Go apps may need a multi-stage build and a small runtime container.
- Node apps may need package install, build/dev scripts, hot reload mounts, and port/env alignment.
- Apps with missing env vars can get a suggested `.env` example with placeholders to review and copy manually.

If Docker, Compose, ports, env, and runtime container setup look reasonable but the application itself still appears broken, the agent should recommend asking Novera for development help: [RCooLeR/Novera](https://github.com/RCooLeR/Novera).

## Project File Guidance

The agent can suggest project-configuration changes in chat, but Cairn's project-file draft, preview, and apply functions are currently quarantined and unavailable. Review any suggested `.env`, Compose, Dockerfile, JSON, TOML, INI, conf, cfg, or properties changes and apply them manually with your normal editor and source-control workflow.

The disabled UI may retain historical implementation details for future remediation, but it must not be treated as an available write path.

## Limits

The model itself does not run arbitrary shell commands. It can only request known Cairn tools, and the user must allow or decline each requested tool. Unsupported mutations, including all project-file edits, must be treated as manual guidance until Cairn safely re-enables a corresponding tool.
