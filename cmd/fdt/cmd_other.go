package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"fdt/internal/apprun/atmapp"
	"fdt/internal/crypto"
	"fdt/internal/e2e"
)

func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	work := fs.String("work", "", "working dir")
	_ = fs.Parse(args)
	dir := *work
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "fdt-demo-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	}
	fixture := filepath.Join(dir, "fixture")
	_ = os.MkdirAll(filepath.Join(fixture, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(fixture, "docs", "readme.txt"), []byte("hello fdt"), 0o644)
	fmt.Println("=== FDT 离线演示 ===")
	rep, err := e2e.Run(context.Background(), e2e.Config{WorkDir: dir, FixtureDir: fixture, Approver: "李四"})
	if err != nil {
		return err
	}
	for _, s := range rep.Steps {
		fmt.Println(s)
	}
	fmt.Printf("\n导出 OK req=%s merkle=%s\n", rep.ReqID, rep.MerkleRoot)
	irep, err := e2e.RunImport(context.Background(), e2e.ImportConfig{WorkDir: filepath.Join(dir, "import"), Approver: "李四"})
	if err != nil {
		return err
	}
	for _, s := range irep.Steps {
		fmt.Println(s)
	}
	fmt.Printf("导入 OK status=%s\n", irep.Status)
	return nil
}

func cmdKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "keys", "output dir")
	_ = fs.Parse(args)
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	for _, name := range []struct{ priv, pub string }{
		{"sk_internal.pem", "pk_internal.pem"},
		{"sk_gateway.pem", "pk_gateway.pem"},
	} {
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			return err
		}
		if err := crypto.WritePrivatePEM(filepath.Join(*out, name.priv), kp.Priv); err != nil {
			return err
		}
		if err := crypto.WritePublicPEM(filepath.Join(*out, name.pub), kp.Pub); err != nil {
			return err
		}
		fmt.Println("wrote", name.priv, name.pub)
	}
	fmt.Println("提示: 完整一键安装请用  sudo fdt install intranet")
	return nil
}

func cmdPortal(args []string) error {
	fmt.Fprintln(os.Stderr, "推荐: sudo fdt install intranet && systemctl status fdt")
	fmt.Fprintln(os.Stderr, "瘦包: 使用 fdt-intranet（或胖包 fdt run）")
	return fmt.Errorf("请使用 fdt run / fdt-intranet（安装后）")
}

func cmdGateway(args []string) error {
	fmt.Fprintln(os.Stderr, "推荐: sudo fdt install gateway --bundle gateway.tgz")
	fmt.Fprintln(os.Stderr, "瘦包: 使用 fdt-gw（或胖包 fdt run）")
	return fmt.Errorf("请使用 fdt run / fdt-gw（安装后）")
}

func cmdATM(args []string) error {
	return atmapp.RunATM(args)
}
