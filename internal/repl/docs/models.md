# Models, effort, and providers

/model fetches the model lists of all configured providers when opened and shows each model with its provider. Choosing a model also makes its provider the default one. A provider that fails to answer is reported and skipped. Up/Down selects
an entry, typing filters it, Left/Right cycles none, low, medium, high, xhigh,
max, and Enter saves the selection. Esc cancels; late fetch results do not reopen
a cancelled menu. Settings changes are disabled while a turn is running.

/login lists configured providers and ends with + Add. Add asks for provider
name, HTTP(S) base URL, and an optional API key. The key is masked, kept in
private settings.toml, and never appended to chat history. Only the Responses API
is supported; a provider configured with the removed chat_completions api_type
fails to load with an explicit error.
Provider selection changes the active session's provider and new-session default;
use /model afterwards to choose a model supported by that provider.

Each session header stores provider identity, model, and effort, but no API key.
Changing defaults updates the current session and new sessions; older sessions
keep their own selection when resumed. A legacy header without selection uses
current defaults and pins them when next saved. Missing provider configuration
raises an error instead of silently switching providers. Provider credentials and
endpoints are resolved from current configuration by provider identity. API
errors for unsupported models/efforts are reported without an automatic fallback.

Transient failures (408, 409, 429, 5xx, timeouts, dropped connections) are
retried up to three attempts, with a visible notice and Retry-After honored.
Retries stop as soon as any output was shown, so text is never duplicated.

Responses supports streaming text, tool argument previews, sequential tool
execution, image input, refusals, and available usage. History is stored as
Responses items verbatim. Unsupported nonstandard provider extensions are not
inferred.

Every shell call, including the prefix, exports session metadata:
RECUR_SESSION_ID, RECUR_MODEL, RECUR_PROVIDER, RECUR_EFFORT and RECUR_CTX_SIZE.
RECUR_CTX_SIZE is the latest recorded request input-token count, including
cached tokens, matching the status-bar estimate; it is empty when unknown, not
the model context limit. Credentials are not exposed. Changing these variables
does not change application settings.
