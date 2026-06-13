//go:build !windows

package main

// Off Windows we don't manage the OS firewall: Linux/macOS don't default-block
// inbound LAN traffic the way Windows Firewall does, and a user-managed
// ufw/firewalld is the operator's call. So there's never anything to fix.
func firewallNeedsFix() bool { return false }

func fixFirewall() error { return nil }
