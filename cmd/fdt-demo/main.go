// Command fdt-demo runs the complete offline data-ferry flow end to end and
// prints every step. It is the fastest way to see the whole system work: only
// the physical USB / scanner / Feishu boundary is mocked; all crypto, hashing,
// Merkle binding, one-shot consume CAS and the audit hash chain are real.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"fdt/internal/e2e"
)

func main() {
	work := flag.String("work", "", "working dir for db/staging/usb (default: a temp dir)")
	fixture := flag.String("fixture", "", "data directory to ferry (default: generated sample)")
	flag.Parse()

	workDir := *work
	if workDir == "" {
		d, err := os.MkdirTemp("", "fdt-demo-")
		if err != nil {
			log.Fatal(err)
		}
		workDir = d
		defer os.RemoveAll(workDir)
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		log.Fatal(err)
	}

	fixtureDir := *fixture
	if fixtureDir == "" {
		var err error
		fixtureDir, err = sampleFixture(filepath.Join(workDir, "fixture"))
		if err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println("=== 离线数据摆渡系统 · 端到端演示 ===")
	fmt.Printf("工作目录: %s\n数据目录: %s\n\n", workDir, fixtureDir)

	rep, err := e2e.Run(context.Background(), e2e.Config{
		FixtureDir: fixtureDir, WorkDir: workDir, Approver: "李四",
	})
	if err != nil {
		log.Fatalf("演示失败: %v", err)
	}

	fmt.Println("--- 流程步骤 ---")
	for _, s := range rep.Steps {
		fmt.Println(s)
	}
	fmt.Println("\n--- 结果汇总 ---")
	fmt.Printf("申请单ID   : %s\n", rep.ReqID)
	fmt.Printf("清单模式   : %s（%d 文件）\n", rep.Mode, rep.FileCount)
	fmt.Printf("Merkle 根  : %s\n", rep.MerkleRoot)
	fmt.Printf("QR#1 / QR#2: %d B / %d B（均单帧）\n", rep.QR1Len, rep.QR2Len)
	fmt.Printf("U盘序列号  : %s，写入 %d 字节，落盘 %d 个文件\n", rep.USBSerial, rep.WrittenBytes, len(rep.WrittenFiles))
	fmt.Printf("申请单状态 : %s\n", rep.FinalStatus)
	fmt.Printf("审计链完整 : %v\n", rep.AuditChainOK)
	fmt.Printf("无盘扫码   : 被拒（%v）\n", rep.NoUSBErr)
	fmt.Printf("QR#2 重放  : 被拒（%v）\n", rep.DoubleScanErr)

	// ---- 导入方向（U盘 → 内网）----
	fmt.Println("\n=== 导入方向演示（U盘 → 隔离区 → 扫描 → 释放）===")
	importWork := filepath.Join(workDir, "import")
	if err := os.MkdirAll(importWork, 0o755); err != nil {
		log.Fatal(err)
	}
	irep, err := e2e.RunImport(context.Background(), e2e.ImportConfig{WorkDir: importWork, Approver: "李四"})
	if err != nil {
		log.Fatalf("导入演示失败: %v", err)
	}
	fmt.Println("--- 流程步骤 ---")
	for _, s := range irep.Steps {
		fmt.Println(s)
	}
	fmt.Println("\n--- 导入结果 ---")
	fmt.Printf("申请单ID   : %s\n", irep.ReqID)
	fmt.Printf("索引文件数 : %d\n", irep.Count)
	fmt.Printf("Merkle 根  : %s\n", irep.MerkleRoot)
	fmt.Printf("释放状态   : %s → %s（%d 文件）\n", irep.Status, irep.ReleasedDir, len(irep.Released))
	fmt.Printf("无盘导入   : 被拒（%v）\n", irep.NoUSBErr)
	fmt.Printf("含可执行   : 被拦截（状态 %s，命中 %v）\n", irep.BlockedStat, irep.BlockedFind)
	fmt.Printf("审计链完整 : %v\n", irep.AuditChainOK)
}

func sampleFixture(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	files := map[string]string{
		"design/top.v":      "module top; input clk; endmodule\n",
		"design/sub/util.v": "module util; endmodule\n",
		"docs/spec.md":      "# confidential design spec\n",
		"data/records.csv":  "id,name\n1,alice\n2,bob\n",
		"release/notes.txt": "release notes\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}
