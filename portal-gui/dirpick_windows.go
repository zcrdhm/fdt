//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// pickDirectory opens a native folder dialog via PowerShell FolderBrowserDialog.
func pickDirectory(title, startDir string) (string, error) {
	if startDir == "" {
		startDir = userPortalRoot()
	}
	// Escape for single-quoted PowerShell string.
	esc := func(s string) string {
		return strings.ReplaceAll(s, "'", "''")
	}
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = '%s'
$d.SelectedPath = '%s'
$d.ShowNewFolderButton = $true
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  Write-Output $d.SelectedPath
}
`, esc(title), esc(startDir))

	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("打开目录对话框失败: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
