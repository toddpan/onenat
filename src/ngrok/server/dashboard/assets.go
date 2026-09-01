package dashboard

import "embed"

// assetsFS embeds the dashboard UI (templates + static) and the install
// script template so ngrokd stays a single self-contained binary.
//
//go:embed assets
var assetsFS embed.FS
