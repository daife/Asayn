package defaults

import "embed"

// FS contains the default ~/.Asayn layout copied on first use.
//
//go:embed default_Asayn/**
var FS embed.FS
