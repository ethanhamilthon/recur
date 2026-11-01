# Models, effort, and providers

## Selecting Models and Effort

`/model` fetches model lists from all configured providers and displays models
with their provider. Choosing a model makes its provider the active default.
Up/Down selects an entry, typing filters models, Left/Right cycles reasoning
effort (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`), and Enter
saves the selection. Esc cancels. Model changes cannot be made while a turn is
running.

`/login` lists configured providers. Choosing one makes it the active provider;
choosing "Add" asks for a provider name, an HTTP(S) base URL and an optional API
key, and writes the new provider to the configuration file.

When `/model` or `/login` changes configuration, Recur atomically patches targeted
raw TOML keys in the selected primary configuration file (`recur.toml` or
`config.toml`), preserving unrelated formatting, comments, values, and sparse inheritance.
The destination configuration file is displayed upon saving. Existing provider
names cannot be overwritten by `/login`; edit the configuration file directly to
change a provider. Selecting a model never touches `[providers]`.

Each session header stores its own provider identity, model, and reasoning
effort. Resuming an older session restores the model selection under which it was
saved.

## Provider Authentication and Secrets

Providers are declared under `[providers.NAME]`; the fields (`base_url`, `api_key`,
`api_key_env`) and their rules are documented once in `recur docs config`.

Secrets given through `api_key_env` are never copied into project files or logged.

A missing or empty `api_key_env` variable raises an error only when that provider
is currently selected, not for unused providers.

## Retries and API Protocol

Recur uses the OpenAI Responses API protocol. History is preserved verbatim as
Responses items.

Transient failures (408, 409, 429, 5xx, timeouts, and dropped connections) are
attempted up to three times in total (two retries) with exponential backoff, honoring any `Retry-After`
header. Retries halt once partial output has been shown to avoid duplicated text.

## Session Metadata

The `RECUR_*` variables exported to shell calls, hooks and status commands are
listed in `recur docs runtime` (shell), `recur docs hooks` (hooks) and
`recur docs interface` (status bar). API keys are never exposed in them.
