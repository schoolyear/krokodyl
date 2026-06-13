//go:build !krokodyl_ble

package main

// bleBuildEnabled is false in the shipped build: Bluetooth is not compiled in,
// so the Bluetooth dependency is absent and the radio path is inert. Build with
// `-tags krokodyl_ble` (and validate on hardware) to enable it.
const bleBuildEnabled = false

// newBLERadio returns the no-op radio. Nearby-Direct still works as guided
// manual hotspot pairing; only the automatic BLE discovery/handshake is off.
func newBLERadio() bleRadio { return noopBLE{} }
