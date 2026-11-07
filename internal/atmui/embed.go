// Package atmui embeds the ATM kiosk touchscreen/web UI.
package atmui

import "embed"

//go:embed index.html app.css app.js jsQR.min.js
var FS embed.FS
