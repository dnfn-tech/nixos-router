package web

import "embed"

// Embedded contains the compiled-in Web UI assets.
//go:embed *
var Embedded embed.FS

