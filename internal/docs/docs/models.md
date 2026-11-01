# Models, effort, and providers

Configuration keys and file rules are in `recur docs config`. This topic covers selection and runtime behavior.

## Selecting Models and Effort

`/model` fetches model lists from all configured providers and shows each model with its provider.
Choosing a model makes its provider the active one. Up and Down move through the list, typing
filters it, Left and Right cycle reasoning effort, Enter saves, and Esc cancels. Model changes
are refused while a turn runs. The selection is written to the primary configuration file.

`/login` lists the configured providers and makes the chosen one active. "Add" asks for a
provider name, an HTTP(S) base URL and an optional API key, then writes the new provider to the
primary configuration file. Existing provider names cannot be overwritten; edit the file to change one.
Selecting a model never touches `[providers]`.

## Saved Selections

Each session header stores its provider, model and reasoning effort. Resuming an older session
restores the selection it was saved with. `/new` starts from the configuration files. `/reload`
keeps the active session's selection.

## Provider Authentication and Secrets

Providers are declared under `[providers.NAME]`. The fields are `base_url`, `api_key` and
`api_key_env`; their rules are in `recur docs config`.

Secrets given through `api_key_env` are never copied into project files, session history or logs.
A missing or empty variable is an error only while its provider is selected. The error text is in
`recur docs troubleshooting`.

## Requests and Retries

Recur speaks the OpenAI Responses API. History is sent as Responses items, unchanged.

Transient failures are retried: HTTP 408, 409, 429 and 5xx responses, and network errors. There are
3 attempts in total, with exponential backoff from 1 second up to 8 seconds. A longer `Retry-After`
from the provider is honored. Retries stop once any output has been shown, so text is never duplicated.

## Session Metadata

The `RECUR_*` variables exported to shell calls, hooks and status commands are listed in `recur docs cli`.
API keys are never exported.
