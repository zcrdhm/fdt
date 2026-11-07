package main

import (
	"flag"
	"fmt"
	"os"

	"fdt/internal/install"
)

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	envPath := fs.String("env", "", "config file (default: /etc/fdt/fdt.env, then ./fdt.env)")
	_ = fs.Parse(args)
	candidates := []string{*envPath, "/etc/fdt/fdt.env", "fdt.env"}
	var env map[string]string
	var err error
	var used string
	for _, p := range candidates {
		if p == "" {
			continue
		}
		env, err = install.LoadEnv(p)
		if err == nil {
			used = p
			break
		}
	}
	if env == nil {
		return fmt.Errorf("读配置失败（试过 /etc/fdt/fdt.env 与 ./fdt.env）: %w", err)
	}
	_ = used
	role := env["ROLE"]
	switch role {
	case "intranet", "portal":
		return runIntranet(env)
	case "gateway", "dmz":
		return runGateway(env)
	case "atm":
		return fmt.Errorf("ATM 是交互式命令，请用: fdt atm …（配置已写入 %s）", *envPath)
	default:
		return fmt.Errorf("fdt.env ROLE=%q 无效", role)
	}
}

func envOr(m map[string]string, k, def string) string {
	if v := m[k]; v != "" {
		return v
	}
	return def
}

func mustEnvFile(m map[string]string, keysDir, name string) string {
	return keysDir + string(os.PathSeparator) + name
}
