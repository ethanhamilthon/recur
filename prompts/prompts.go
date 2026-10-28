// Package prompts holds the editable default prompt files, embedded into the binary.
package prompts

import _ "embed"

//go:embed prefix.sh
var Prefix string

//go:embed system.md
var System string
