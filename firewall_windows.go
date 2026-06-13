//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// firewallRuleName is the inbound allow rule krokodyl manages for itself.
const firewallRuleName = "krokodyl"

// swHide hides the spawned console window (the UAC consent dialog still shows).
const swHide int32 = 0

// firewallNeedsFix reports whether LAN peers are likely being blocked from
// reaching us by Windows Firewall — i.e. there is no inbound allow rule for THIS
// executable. Windows default-blocks inbound and (on this and many machines)
// NotifyOnListen is off, so it never shows the "allow this app?" prompt and just
// drops everything silently. The UI uses this to offer a one-click fix. The
// check needs no elevation.
func firewallNeedsFix() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return !firewallRuleExists(exe)
}

// firewallRuleExists reports whether a krokodyl rule already allows THIS exe
// inbound. `netsh ... show rule` works without elevation and prints
// "No rules match" (exiting non-zero) when absent; we additionally require the
// rule to name the current exe so a stale rule for an old build path doesn't
// count as covering us.
func firewallRuleExists(exe string) bool {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "show", "rule",
		"name="+firewallRuleName, "dir=in", "verbose")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(exe))
}

// fixFirewall resets any stale krokodyl rule and adds a fresh PROGRAM-scoped
// inbound allow rule for THIS exe (one rule covers every port it ever listens
// on). Modifying firewall rules requires admin, so it runs one elevated command
// — the user gets a single UAC prompt, triggered by their click on the in-app
// "Fix" button (never a surprise at startup). Returns an error if the prompt is
// declined or the rule doesn't appear afterward.
func fixFirewall() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	// cmd /c chains delete (reset) then add in a single elevation; the delete's
	// "no rules match" noise is swallowed so a first-time add still proceeds.
	cmdline := fmt.Sprintf(
		`/c netsh advfirewall firewall delete rule name="%s">nul 2>&1 & netsh advfirewall firewall add rule name="%s" dir=in action=allow program="%s" enable=yes profile=any`,
		firewallRuleName, firewallRuleName, exe,
	)
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString("cmd.exe")
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		return err
	}
	// ShellExecute returns an error if the UAC prompt is declined/cancelled.
	if err := windows.ShellExecute(0, verb, file, params, nil, swHide); err != nil {
		return fmt.Errorf("firewall change was not authorized: %w", err)
	}
	// The elevated netsh runs asynchronously; poll until the rule shows up.
	for i := 0; i < 20; i++ {
		time.Sleep(400 * time.Millisecond)
		if firewallRuleExists(exe) {
			return nil
		}
	}
	return fmt.Errorf("firewall rule did not appear after the elevation")
}
