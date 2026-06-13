//go:build !windows

package main

import "syscall"

// setReuseAddr sets SO_REUSEADDR so multiple sockets can bind the multicast
// port (lets krokodyl coexist with the official LocalSend app, and matches the
// behavior ListenMulticastUDP used to give us).
func setReuseAddr(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
