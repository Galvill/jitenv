package shell

import _ "embed"

//go:embed snippets/bash.sh
var Bash string

//go:embed snippets/zsh.sh
var Zsh string
