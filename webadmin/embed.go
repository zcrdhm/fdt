// Package webadmin embeds the gateway operator console, registration and tenant console UIs.
package webadmin

import "embed"

//go:embed admin.html admin.js admin.css register.html register.js console.html console.js console.css logo.png favicon.ico
var FS embed.FS
