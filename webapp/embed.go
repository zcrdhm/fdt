// Package webapp embeds the Feishu H5 entry (scan QR#1 → submit → show QR#2).
// The DMZ gateway serves it at /app (no trailing slash) so Feishu
// requestAuthCode 重定向 URL can match https://<gateway>/app exactly.
package webapp

import "embed"

//go:embed index.html app.js app.css
var FS embed.FS
