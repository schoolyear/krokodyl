package main

import "errors"

// bleRadio bootstraps offline pairing over Bluetooth LE: one device hosts
// (advertises + serves its handshake), the other joins (scans + reads it +
// writes its own back). Because a single process can be BLE central OR
// peripheral but not both at once (tinygo/bluetooth constraint), the negotiated
// role picks exactly one of host()/join() per device.
//
// The default build ships noopBLE (no Bluetooth — the feature degrades to a
// guided manual hotspot). The real tinygo-backed driver compiles only with
// `-tags krokodyl_ble`; it is gated off until validated on real hardware, so
// shipped binaries never depend on an unverified radio path or pull the
// Bluetooth dependency into the release build.
type bleRadio interface {
	// available reports whether Bluetooth hardware + OS permission are usable.
	available() bool
	// host advertises self and blocks until a joiner completes the handshake
	// (or stop fires); returns the joiner's handshake.
	host(stop <-chan struct{}, self bleHandshake) (bleHandshake, error)
	// join scans for a krokodyl host, reads its handshake, writes self, and
	// returns the host's handshake (carrying SSID/PSK/control-port/code).
	join(stop <-chan struct{}, self bleHandshake) (bleHandshake, error)
	// close releases any radio resources.
	close()
}

// errBLEUnavailable is returned by every operation when Bluetooth is not
// compiled in or not usable. Callers treat it as "fall back to manual hotspot",
// never as a hard error.
var errBLEUnavailable = errors.New("bluetooth is not available on this build/device")

// noopBLE is the default radio: no Bluetooth. It exists so the rest of the
// Nearby-Direct stack can be wired, tested, and shipped without a radio.
type noopBLE struct{}

func (noopBLE) available() bool { return false }

func (noopBLE) host(<-chan struct{}, bleHandshake) (bleHandshake, error) {
	return bleHandshake{}, errBLEUnavailable
}

func (noopBLE) join(<-chan struct{}, bleHandshake) (bleHandshake, error) {
	return bleHandshake{}, errBLEUnavailable
}

func (noopBLE) close() {}
