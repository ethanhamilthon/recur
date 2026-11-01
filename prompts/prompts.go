// Package prompts holds the embedded default configuration.
package prompts

import _ "embed"

//go:embed config.toml
var Config string

// Legacy compatibility variables.
var System = "You are Recur, a coding agent"
var Prefix = ""
