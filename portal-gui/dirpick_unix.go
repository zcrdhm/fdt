//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// pickDirectory opens a native folder dialog; cancel returns ("", nil).
func pickDirectory(title, startDir string) (string, error) {
	if startDir == "" {
		startDir = userPortalRoot()
	}
	if path, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--file-selection", "--directory", "--title=" + title}
		if startDir != "" {
			args = append(args, "--filename="+startDir+"/")
		}
		out, err := exec.Command(path, args...).Output()
		if err != nil {
			// user cancel → exit 1
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				return "", nil
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		args := []string{"--getexistingdirectory", startDir, title}
		out, err := exec.Command(path, args...).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				return "", nil
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	return "", fmt.Errorf("未找到 zenity/kdialog，请手动填写路径（可安装 zenity）")
}
