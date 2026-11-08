// Command fdt is the single binary for build, install and run.
//
//	fdt install intranet|gateway|atm   # one-click role setup
//	fdt run                            # start from /etc/fdt/fdt.env
//	fdt demo|keygen|portal|gateway|atm # advanced / legacy entry points
package main

import (
	"fmt"
	"os"

	"fdt/internal/buildinfo"
)

const usage = `FDT — 离线数据摆渡

推荐按角色用瘦包（make release）：
  fdt-gw          DMZ 网关
  fdt-intranet    内网 Portal
  fdt-atm         ATM 终端
本命令为全能包（含 install / 三角色），也可单独使用。

一键部署：
  sudo ./fdt install intranet [--tenant acme]   # 同目录有 fdt-intranet 则优先装瘦包
  sudo ./fdt install gateway --bundle gateway.tgz --domain gw.example.com --email ops@x.com
  sudo ./fdt install gateway --tenants tenants.json [--mock]
  sudo ./fdt install atm --bundle atm-01.tgz --server http://10.0.0.5:8081

日常：
  sudo systemctl status fdt
  fdt run                  # 按 /etc/fdt/fdt.env 的 ROLE 启动
  fdt atm -ui              # ATM 触屏
  fdt demo
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "install":
		err = cmdInstall(args)
	case "run":
		err = cmdRun(args)
	case "demo":
		err = cmdDemo(args)
	case "keygen":
		err = cmdKeygen(args)
	case "portal":
		err = cmdPortal(args)
	case "gateway":
		err = cmdGateway(args)
	case "atm":
		err = cmdATM(args)
	case "version":
		fmt.Println(buildinfo.Summary("fdt"))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
