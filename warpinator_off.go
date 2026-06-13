//go:build !krokodyl_warpinator

package main

// warpBuildEnabled is false in shipped builds: the Warpinator adapter (and its
// gRPC dependency) is not compiled in. Build with `-tags krokodyl_warpinator`
// to enable it (see .claude/spikes/warpinator-runbook.md for the auth/discovery
// work still needed for real-app interop).
const warpBuildEnabled = false

// startWarpinator is a no-op without the build tag.
func (a *App) startWarpinator(string) func() { return nil }
