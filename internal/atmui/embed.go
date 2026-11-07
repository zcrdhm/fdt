// Package atmui embeds the ATM kiosk touchscreen/web UI.
package atmui

import "embed"

//go:embed index.html app.css app.js jsQR.min.js logo.png favicon.ico
var FS embed.FS
