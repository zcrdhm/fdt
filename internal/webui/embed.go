// Package webui embeds the Portal frontend into the binary so the UI is served
// regardless of the process working directory (no dependency on a relative
// web/ path). Deployments can still override with -web-dir for a custom skin.
package webui

import "embed"

//go:embed index.html admin.html app.css app.js admin.js logo.png favicon.ico
var FS embed.FS
