// Package webapp embeds the Feishu H5 entry (scan QR#1 → submit → show QR#2).
// The DMZ gateway serves it at /app/ so the Feishu「网页应用」homepage can be
// https://<gateway>/app/ on the same origin as /submit and /qr2.
package webapp

import "embed"

//go:embed index.html app.js app.css
var FS embed.FS
