package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type permResult struct {
	OK       bool   `json:"ok"`
	Path     string `json:"path"`
	Created  bool   `json:"created"`
	Message  string `json:"message"`
	CanRead  bool   `json:"canRead"`
	CanWrite bool   `json:"canWrite"`
}

func resolveSetupDir(raw, fallback string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return fallback
	}
	if strings.HasPrefix(p, "~"+string(os.PathSeparator)) || p == "~" {
		home, _ := os.UserHomeDir()
		if home != "" {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// testDirPermissions creates the directory if needed and verifies read/write.
func testDirPermissions(path string) permResult {
	path = filepath.Clean(path)
	res := permResult{Path: path}
	if path == "" || path == "." {
		res.Message = "路径为空"
		return res
	}
	st, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			res.Message = "无法访问: " + err.Error()
			return res
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			res.Message = "无法创建目录: " + err.Error()
			return res
		}
		res.Created = true
	} else if !st.IsDir() {
		res.Message = "路径已存在但不是目录"
		return res
	}

	probe := filepath.Join(path, ".fdt-perm-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	const payload = "fdt-perm-ok\n"
	if err := os.WriteFile(probe, []byte(payload), 0o600); err != nil {
		res.Message = "无写权限: " + err.Error()
		_, e := os.ReadDir(path)
		res.CanRead = e == nil
		return res
	}
	res.CanWrite = true
	b, err := os.ReadFile(probe)
	_ = os.Remove(probe)
	if err != nil {
		res.Message = "可写但读失败: " + err.Error()
		return res
	}
	if string(b) != payload {
		res.Message = "读写内容不一致"
		return res
	}
	res.CanRead = true
	res.OK = true
	if res.Created {
		res.Message = "目录已创建，读写正常"
	} else {
		res.Message = "读写正常"
	}
	return res
}
