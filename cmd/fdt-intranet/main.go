// Command fdt-intranet is the slim air-gapped Portal + ATM data-plane binary.
package main

import (
	"flag"
	"fmt"
	"os"

	"fdt/internal/apprun"
	"fdt/internal/apprun/inapp"
	"fdt/internal/buildinfo"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "version" || args[0] == "-version" || args[0] == "--version") {
		fmt.Println(buildinfo.Summary("fdt-intranet"))
		return
	}
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("fdt-intranet", flag.ExitOnError)
	envPath := fs.String("env", "", "config file (default: /etc/fdt/fdt.env)")
	_ = fs.Parse(args)

	env, used, err := apprun.LoadEnvFile(*envPath, apprun.DefaultEnvCandidates()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = used
	if err := apprun.RequireRole(env, "intranet", "portal"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := inapp.RunIntranet(env); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
