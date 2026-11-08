// Package buildinfo holds release metadata stamped into binaries and UIs.
package buildinfo

// Version is the product release version (overridable via -ldflags -X).
var Version = "1.0.1"

// Copyright is shown in Portal / ATM UIs and CLI version output.
var Copyright = "© fasteda.cn"

// Summary is a one-line banner, e.g. "FDT 1.0.1  © fasteda.cn".
func Summary(product string) string {
	if product == "" {
		product = "FDT"
	}
	return product + " " + Version + "  " + Copyright
}
