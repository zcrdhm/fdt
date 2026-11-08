// Command fdt-gw is the slim DMZ gateway binary (no Portal/ATM UI).
package main

import (
	"flag"
	"fmt"
	"os"

	"fdt/internal/apprun"
	"fdt/internal/apprun/gwapp"
)

func main() {
	args := os.Args[1:]
	// Compat with systemd: ExecStart=…/fdt run  (slim binary installed as fdt)
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("fdt-gw", flag.ExitOnError)
	envPath := fs.String("env", "", "config file (default: /etc/fdt/fdt.env)")
	_ = fs.Parse(args)

	env, used, err := apprun.LoadEnvFile(*envPath, apprun.DefaultEnvCandidates()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = used
	if err := apprun.RequireRole(env, "gateway", "dmz"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := gwapp.RunGateway(env); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
