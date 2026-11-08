//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const winTaskName = "FDT Portal"

func installPortalService(envPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	envPath, err = filepath.Abs(envPath)
	if err != nil {
		return err
	}
	// ONLOGON task — no Administrator required for current user.
	tr := fmt.Sprintf(`"%s" -service -no-browser -env "%s"`, exe, envPath)
	args := []string{
		"/Create", "/TN", winTaskName, "/TR", tr,
		"/SC", "ONLOGON", "/RL", "LIMITED", "/F",
	}
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create: %v\n%s", err, out)
	}
	return startPortalService()
}

func uninstallPortalService() error {
	_ = exec.Command("schtasks", "/End", "/TN", winTaskName).Run()
	cmd := exec.Command("schtasks", "/Delete", "/TN", winTaskName, "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks delete: %v\n%s", err, out)
	}
	return nil
}

func startPortalService() error {
	cmd := exec.Command("schtasks", "/Run", "/TN", winTaskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks run: %v\n%s", err, out)
	}
	return nil
}

func portalServiceInstalled() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", winTaskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}

func portalServiceRunning() bool {
	// Task Scheduler "Running" is awkward; probe listen via process name.
	cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq "+filepath.Base(mustExeName()), "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(filepath.Base(mustExeName())))
}

func mustExeName() string {
	exe, err := os.Executable()
	if err != nil {
		return "fdt-portal-gui.exe"
	}
	return filepath.Base(exe)
}

func serviceManageHint() string {
	return "管理任务: schtasks /Query /TN \"FDT Portal\"\n" +
		"停止: schtasks /End /TN \"FDT Portal\"\n" +
		"卸载: fdt-portal-gui.exe -uninstall-service"
}
