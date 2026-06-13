//go:build !krokodyl_kdeconnect

package main

// kdeBuildEnabled is false in shipped builds: the KDE Connect adapter is not
// compiled in. Build with `-tags krokodyl_kdeconnect` (and finish pairing/cert
// pinning) to enable it.
const kdeBuildEnabled = false

// startKDEConnect is a no-op without the build tag. Returns nil so the caller
// stores no stop func.
func (a *App) startKDEConnect(string) func() { return nil }
