// Command fdt-atm is the slim ATM kiosk / CLI binary.
// Compatible with fat-binary scripts: accepts an optional leading "atm" word.
package main

import (
	"fmt"
	"os"

	"fdt/internal/apprun/atmapp"
	"fdt/internal/buildinfo"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "version" || args[0] == "-version" || args[0] == "--version") {
		fmt.Println(buildinfo.Summary("fdt-atm"))
		return
	}
	if len(args) > 0 && args[0] == "atm" {
		args = args[1:]
	}
	if err := atmapp.RunATM(args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
