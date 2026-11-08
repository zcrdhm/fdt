//go:build windows

package main

func runningAsRoot() bool {
	// GUI is designed for standard users; Windows USB=DRIVE_REMOVABLE needs no admin.
	return false
}
