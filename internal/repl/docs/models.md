# Models, effort, and providers

## Selecting Models and Effort

`/model` fetches model lists from all configured providers and displays models
with their provider. Choosing a model makes its provider the active default.
Up/Down selects an entry, typing filters models, Left/Right cycles reasoning
effort (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`), and Enter
saves the selection. Esc cancels. Model changes cannot be made while a turn is
running.

`/login` lists configured providers and allows adding or updating credentials
with a provider name, HTTP(S) base URL, and API key.

When `/model` or `/login` changes configuration, Recur atomically patches the
raw TOML keys in the selected primary configuration file (`recur.toml` or
`config.toml`), preserving unrelated values, comments, and sparse inheritance.
The destination configuration file is displayed upon saving.

Each session header stores its own provider identity, model, and reasoning
effort. Resuming an older session restores the model selection under which it was
saved.

## Provider Authentication and Secrets

Providers are declared in `recur.toml` or `config.toml` under `[providers.NAME]`:

```toml
[providers.openrouter]
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"

[providers.custom]
base_url = "https://api.example.com/v1"
api_key = "secret-token"
```

Authentication options:
- `api_key`: literal secret string in the configuration file.
- `api_key_env`: name of an environment variable containing the secret API key.
  Recur resolves the key at runtime from the host environment plus `[env]`.

Secrets are never copied into project files, never logged, and never persisted
to disk when using `api_key_env`. Specifying both `api_key` and `api_key_env`
with nonempty values causes a configuration conflict error. An explicit empty
string (`api_key = ""` or `api_key_env = ""`) clears an inherited setting.

A missing or empty `api_key_env` variable raises an error only when that provider
is currently selected, not for unused providers.

## Retries and API Protocol

Recur uses the OpenAI Responses API protocol. History is preserved verbatim as
Responses items.

Transient failures (408, 409, 429, 5xx, timeouts, and dropped connections) are
retried up to three times with exponential backoff, honoring any `Retry-After`
header. Retries halt once partial output has been shown to avoid duplicated text.

## Session Metadata

Every tool call and status command exports session metadata:
- `RECUR_SESSION_ID`: active session identifier.
- `RECUR_MODEL`: active model name.
- `RECUR_PROVIDER`: active provider name.
- `RECUR_EFFORT`: active reasoning effort.
- `RECUR_CTX_SIZE`: latest request input token count (including cached tokens),
  matching the status bar estimate; empty when unknown.
- `RECUR_ENDPOINT`: session socket path.

API keys and provider secrets are never exposed in environment variables or
metadata. Modifying metadata variables inside a shell command does not alter
Recur settings.
