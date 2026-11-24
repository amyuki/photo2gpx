//go:build windows

package main

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

// windowsVersion returns e.g. "Windows 11 23H2 (build 22631)".
func windowsVersion() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	name, _, _ := k.GetStringValue("ProductName")
	disp, _, _ := k.GetStringValue("DisplayVersion")
	build, _, _ := k.GetStringValue("CurrentBuild")
	if b, err := strconv.Atoi(build); err == nil && b >= 22000 && len(name) >= 10 && name[:10] == "Windows 10" {
		name = "Windows 11" + name[10:] // the registry still says "Windows 10" on Windows 11
	}
	return fmt.Sprintf("%s %s (build %s)", name, disp, build)
}
