# Recur — a minimalist AI agent for coding and automation

Recur is configured with a single file: `recur.toml`. Put it in your project root and describe what the agent should know: skills, MCP, commands, files, and anything else.

You can also change the system prompt, model, and provider — per project or per agent. For example, create separate Planner, Builder, and Reviewer agents.

Recur exists so you can build the agent's context differently for each project and use case.

## Recur vs. other harnesses

**Pi, DSH, OpenCode:** Recur is configured through one file. Customizing Pi/DSH/OpenCode means maintaining a whole codebase, which hurts even more when you need many different profiles.

**Claude Code, Codex, Copilot:** Recur works with any AI provider. You can change its tools, system prompt, and behavior. The base version has nothing extra.

Recur doesn't tell you how to code. It lets you build the context where an AI codes best.

## Quick Start

1. Install:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/ethanhamilthon/recur/main/scripts/install.sh | sh
   ```

   The binary goes to `~/.local/bin/recur` (override with `RECUR_BINDIR`; pin a version with `RECUR_VERSION`). Make sure that directory is in your `PATH`. Supports macOS and Linux (amd64, arm64).

   Or build from source (Go 1.26+):

   ```sh
   git clone https://github.com/ethanhamilthon/recur && cd recur
   make install
   ```

2. Run `recur` in your project directory.
3. Run `/login` to sign in to a provider.
4. Run `/model` to pick a model.

## recur.toml

`recur.toml` is the only file you need to think about. All configuration lives here: UI, providers/models, hooks, system prompt, agent behavior, and more.

By default Recur reads `~/.recur/config.toml`. If the current directory has a `recur.toml`, it is applied on top of the global config. You can also pass a config explicitly: `recur -config example.toml`.

A minimal config:

```toml
provider = "openrouter"
model = "anthropic/claude-sonnet-4"
effort = "medium"

# Custom system prompt (optional)
system = "You are a concise coding assistant."

# Any provider that speaks the OpenAI Responses API
[providers.openrouter]
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"  # better than a literal api_key in a repo

# Add extra context at session start: stdout is sent to the agent
[hooks.context]
events = ["session_start"]
command = "cat AGENTS.md"
send_output = true
```

Write only the keys you want to change; everything else keeps its default. Unknown keys are rejected at startup. More options: `recur docs config`.

## License

MIT

## Security

Recur runs in full YOLO mode. If you don't trust your models, use Docker or run Recur in a secure sandbox.
