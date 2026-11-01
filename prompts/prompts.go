// Package prompts holds the embedded default configuration.
package prompts

import _ "embed"

//go:embed config.toml
var Config string
