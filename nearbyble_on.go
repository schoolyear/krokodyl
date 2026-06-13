//go:build krokodyl_ble

package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"tinygo.org/x/bluetooth"
)

// Real Bluetooth LE driver, compiled only with `-tags krokodyl_ble`.
//
// HARDWARE-VALIDATION PENDING: this code is written against the documented
// tinygo/bluetooth API and is compiled in CI, but its RUNTIME behaviour
// (advertising, scanning, GATT read/write, MTU negotiation) has NOT been
// verified on two physical radios. Do not enable in shipped builds until a
// two-machine test passes. macOS cannot act as a BLE peripheral (tinygo
// limitation), so a Mac can only ever take the JOIN role.

const bleBuildEnabled = true

// krokodyl's BLE service + handshake characteristic. Fixed random UUIDs so two
// krokodyl instances recognise each other and nothing else does.
var (
	bleServiceUUID = bluetooth.NewUUID([16]byte{
		0x6b, 0x72, 0x6f, 0x6b, 0x6f, 0x64, 0x79, 0x6c,
		0x00, 0x01, 0xde, 0xad, 0xbe, 0xef, 0x42, 0x79,
	})
	bleHandshakeCharUUID = bluetooth.NewUUID([16]byte{
		0x6b, 0x72, 0x6f, 0x6b, 0x6f, 0x64, 0x79, 0x6c,
		0x00, 0x02, 0xde, 0xad, 0xbe, 0xef, 0x42, 0x79,
	})
)

func newBLERadio() bleRadio { return &tinygoBLE{} }

type tinygoBLE struct {
	once    sync.Once
	enabled bool
}

// ensure enables the adapter exactly once. sync.Once makes it safe to call
// from the frontend goroutine (available()) concurrently with host()/join().
func (b *tinygoBLE) ensure() error {
	b.once.Do(func() {
		b.enabled = bluetooth.DefaultAdapter.Enable() == nil
	})
	if !b.enabled {
		return fmt.Errorf("%w: could not enable BLE adapter", errBLEUnavailable)
	}
	return nil
}

func (b *tinygoBLE) available() bool {
	return b.ensure() == nil
}

func (b *tinygoBLE) close() {}

// host advertises the krokodyl service and serves a single read+write
// characteristic: the joiner reads our handshake and writes its own back.
// Unsupported on macOS (no peripheral role).
func (b *tinygoBLE) host(stop <-chan struct{}, self bleHandshake) (bleHandshake, error) {
	if runtime.GOOS == "darwin" {
		return bleHandshake{}, fmt.Errorf("%w: macOS cannot advertise over BLE — take the join role", errBLEUnavailable)
	}
	if err := b.ensure(); err != nil {
		return bleHandshake{}, err
	}
	payload, err := encodeHandshake(self)
	if err != nil {
		return bleHandshake{}, err
	}

	adapter := bluetooth.DefaultAdapter
	peerCh := make(chan bleHandshake, 1)

	var char bluetooth.Characteristic
	svcErr := adapter.AddService(&bluetooth.Service{
		UUID: bleServiceUUID,
		Characteristics: []bluetooth.CharacteristicConfig{{
			Handle: &char,
			UUID:   bleHandshakeCharUUID,
			Value:  payload,
			Flags:  bluetooth.CharacteristicReadPermission | bluetooth.CharacteristicWritePermission,
			WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
				h, err := decodeHandshake(value)
				if err != nil {
					logrus.WithError(err).Debug("ble: ignoring malformed joiner handshake")
					return
				}
				select {
				case peerCh <- h:
				default:
				}
			},
		}},
	})
	if svcErr != nil {
		return bleHandshake{}, fmt.Errorf("ble add service: %w", svcErr)
	}

	adv := adapter.DefaultAdvertisement()
	if err := adv.Configure(bluetooth.AdvertisementOptions{
		LocalName:    "krokodyl",
		ServiceUUIDs: []bluetooth.UUID{bleServiceUUID},
	}); err != nil {
		return bleHandshake{}, fmt.Errorf("ble configure advertisement: %w", err)
	}
	if err := adv.Start(); err != nil {
		return bleHandshake{}, fmt.Errorf("ble start advertisement: %w", err)
	}
	defer adv.Stop()

	select {
	case peer := <-peerCh:
		return peer, nil
	case <-stop:
		return bleHandshake{}, errBLEUnavailable
	}
}

// join scans for a krokodyl host, connects, reads its handshake, and writes
// ours back.
func (b *tinygoBLE) join(stop <-chan struct{}, self bleHandshake) (bleHandshake, error) {
	if err := b.ensure(); err != nil {
		return bleHandshake{}, err
	}
	payload, err := encodeHandshake(self)
	if err != nil {
		return bleHandshake{}, err
	}
	adapter := bluetooth.DefaultAdapter

	// StopScan from inside the callback is the documented-safe call site; it
	// makes Scan() return so the goroutine exits. The stop/timeout branches
	// below also call StopScan to unblock it. (Not WaitGroup-joined: the gated
	// driver isn't retried in a tight loop, but a future auto-retry loop should
	// coordinate this — hardware-validation TODO.)
	resultCh := make(chan bluetooth.ScanResult, 1)
	go func() {
		_ = adapter.Scan(func(a *bluetooth.Adapter, res bluetooth.ScanResult) {
			if res.HasServiceUUID(bleServiceUUID) {
				_ = a.StopScan()
				select {
				case resultCh <- res:
				default:
				}
			}
		})
	}()

	var res bluetooth.ScanResult
	select {
	case res = <-resultCh:
	case <-stop:
		_ = adapter.StopScan()
		return bleHandshake{}, errBLEUnavailable
	case <-time.After(60 * time.Second):
		_ = adapter.StopScan()
		return bleHandshake{}, fmt.Errorf("%w: no krokodyl host found", errBLEUnavailable)
	}

	device, err := adapter.Connect(res.Address, bluetooth.ConnectionParams{})
	if err != nil {
		return bleHandshake{}, fmt.Errorf("ble connect: %w", err)
	}
	defer device.Disconnect()

	services, err := device.DiscoverServices([]bluetooth.UUID{bleServiceUUID})
	if err != nil {
		return bleHandshake{}, fmt.Errorf("ble discover service: %w", err)
	}
	if len(services) == 0 {
		return bleHandshake{}, fmt.Errorf("ble: krokodyl service not found on peer")
	}
	chars, err := services[0].DiscoverCharacteristics([]bluetooth.UUID{bleHandshakeCharUUID})
	if err != nil {
		return bleHandshake{}, fmt.Errorf("ble discover characteristic: %w", err)
	}
	if len(chars) == 0 {
		return bleHandshake{}, fmt.Errorf("ble: handshake characteristic not found on peer")
	}

	buf := make([]byte, maxHandshakeBytes)
	n, err := chars[0].Read(buf)
	if err != nil {
		return bleHandshake{}, fmt.Errorf("ble read host handshake: %w", err)
	}
	host, err := decodeHandshake(buf[:n])
	if err != nil {
		return bleHandshake{}, fmt.Errorf("ble decode host handshake: %w", err)
	}
	if _, err := chars[0].WriteWithoutResponse(payload); err != nil {
		return bleHandshake{}, fmt.Errorf("ble write joiner handshake: %w", err)
	}
	return host, nil
}
