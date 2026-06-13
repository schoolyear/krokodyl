//go:build krokodyl_kdeconnect

package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// KDE Connect receive adapter, compiled only with `-tags krokodyl_kdeconnect`.
//
// HARDWARE-VALIDATION PENDING: written against the documented KDE Connect
// protocol and unit-tested at the codec layer (kdeconnect_proto.go), but the
// live handshake — UDP identity exchange, the TCP+TLS upgrade direction, device
// pairing/cert-trust, and the separate payload socket — has NOT been verified
// against the real KDE Connect app on a paired device. Pairing here is
// simplified to the same human-accept consent as every other krokodyl receive
// path (the nearby:offer prompt); persistent per-device cert trust is a TODO.
//
// SECURITY: the payload TLS connection is pinned to the exact certificate the
// peer presents on the control connection (same device serves both), so a
// third party cannot MITM the payload port — no blanket InsecureSkipVerify.
// What's still missing for full parity with the real app is PERSISTENT device
// pairing: we currently trust-on-first-use whoever connects (the control conn
// accepts any client cert) rather than verifying against a previously-paired,
// stored cert. That's why the tag stays off by default — finish persistent
// pairing + validate against the KDE Connect app before shipping it enabled.

const kdeBuildEnabled = true

// kcConnTimeout bounds an idle peer connection so a stalled handshake can't
// leak a goroutine for the receiver's lifetime.
const kcConnTimeout = 30 * time.Second

type kdeConnectReceiver struct {
	dest     string
	identity kcIdentity
	tlsCert  tls.Certificate
	onOffer  func(alias, addr string, files []string, size int64) bool
	onFile   func(name string, size int64)

	udp    *net.UDPConn
	tcp    net.Listener
	port   int
	stopCh chan struct{}
}

// newKDEConnectReceiver listens on the given TCP port (0 = OS-assigned, used by
// tests; production passes kcDefaultPort). The bound port is exposed via .port.
func newKDEConnectReceiver(dest, deviceID, name string, port int, onOffer func(string, string, []string, int64) bool, onFile func(string, int64)) (*kdeConnectReceiver, error) {
	cert, err := ephemeralCertificate()
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: certificate: %w", err)
	}
	tcp, err := tls.Listen("tcp", fmt.Sprintf(":%d", port), &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12, // KDE Connect peers vary; 1.2 floor
		ClientAuth:   tls.RequireAnyClientCert,
	})
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: listen tcp %d: %w", port, err)
	}
	bound := tcp.Addr().(*net.TCPAddr).Port
	r := &kdeConnectReceiver{
		dest:     dest,
		identity: ourKCIdentity(deviceID, name, bound),
		tlsCert:  cert,
		onOffer:  onOffer,
		onFile:   onFile,
		tcp:      tcp,
		port:     bound,
		stopCh:   make(chan struct{}),
	}
	go r.acceptLoop()
	r.startUDP()
	return r, nil
}

func (r *kdeConnectReceiver) startUDP() {
	addr := &net.UDPAddr{Port: r.port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		logrus.WithError(err).Debug("kdeconnect: udp identity listen unavailable")
		return
	}
	r.udp = conn
	go r.udpLoop(conn)
	go func() { <-r.stopCh; conn.Close() }()
}

// udpLoop reads identity broadcasts; for a real implementation we would dial
// back and start the TLS handshake. Left as discovery-only here (the inbound
// TCP path below is what we validate first).
func (r *kdeConnectReceiver) udpLoop(conn *net.UDPConn) {
	buf := make([]byte, kcMaxPacketBytes)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if p, err := decodeKCPacket(buf[:n]); err == nil {
			if _, err := parseKCIdentity(p); err == nil {
				logrus.Debug("kdeconnect: heard a device identity broadcast")
			}
		}
	}
}

func (r *kdeConnectReceiver) acceptLoop() {
	for {
		conn, err := r.tcp.Accept()
		if err != nil {
			return // listener closed
		}
		go r.handle(conn)
	}
}

// handle processes one peer connection: exchange identity, then read packets;
// a share.request triggers consent and a payload fetch.
func (r *kdeConnectReceiver) handle(conn net.Conn) {
	defer conn.Close()
	peerHost := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(peerHost); err == nil {
		peerHost = host
	}

	// Bound an idle peer: close() shuts the listener but not accepted conns, so
	// without a deadline a peer that connects and stalls leaks this goroutine.
	_ = conn.SetDeadline(time.Now().Add(kcConnTimeout))

	// Capture the cert the peer presents on this control connection (KDE
	// Connect requires a client cert). The payload connection is pinned to the
	// SAME cert — the same device serves both — so a third party can't MITM the
	// payload port. This replaces the old blanket InsecureSkipVerify.
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return
	}
	if err := tc.Handshake(); err != nil {
		return
	}
	peerCerts := tc.ConnectionState().PeerCertificates
	if len(peerCerts) == 0 {
		logrus.Warn("kdeconnect: peer presented no certificate; refusing")
		return
	}
	peerFP := certFingerprint(peerCerts[0].Raw)

	if line, err := encodeKCPacket(kcPacketIdentity, r.identity); err == nil {
		if _, err := conn.Write(line); err != nil {
			return
		}
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), kcMaxPacketBytes)
	var peerName string
	for scanner.Scan() {
		p, err := decodeKCPacket(scanner.Bytes())
		if err != nil {
			continue
		}
		switch p.Type {
		case kcPacketIdentity:
			if id, err := parseKCIdentity(p); err == nil {
				peerName = id.DeviceName
			}
		case kcPacketShareReq:
			r.handleShare(p, peerName, peerHost, peerFP)
		}
	}
}

func (r *kdeConnectReceiver) handleShare(p kcPacket, peerName, peerHost, peerFP string) {
	share, err := parseKCShareRequest(p)
	if err != nil {
		return
	}
	alias := sanitizeDisplayName(peerName)
	if alias == "" {
		alias = "KDE Connect device"
	}
	if !r.onOffer(alias, peerHost, []string{sanitizeDisplayName(share.Filename)}, share.PayloadSize) {
		return
	}

	// Payload arrives on a separate TLS socket on the sender. Pin it to the
	// cert the peer presented on the control connection — same device, so any
	// other cert is a MITM and is rejected.
	payAddr := net.JoinHostPort(peerHost, strconv.Itoa(share.PayloadInfo.Port))
	payConn, err := tls.Dial("tcp", payAddr, &tls.Config{
		Certificates:          []tls.Certificate{r.tlsCert},
		InsecureSkipVerify:    true, //nolint:gosec // self-signed; pinned to the control-conn cert below
		VerifyPeerCertificate: pinFingerprint(peerFP),
		MinVersion:            tls.VersionTLS12,
	})
	if err != nil {
		logrus.WithError(err).Warn("kdeconnect: could not open payload connection")
		return
	}
	defer payConn.Close()

	// PayloadSize is validated > 0 at parse time; cap the read at exactly that
	// so a lying sender can't stream past the declared size.
	src := io.LimitReader(payConn, share.PayloadSize)
	name, n, err := saveUploadedFile(r.dest, share.Filename, src)
	if err != nil {
		logrus.WithError(err).Warn("kdeconnect: could not save payload")
		return
	}
	r.onFile(name, n)
}

func (r *kdeConnectReceiver) close() {
	close(r.stopCh)
	if r.tcp != nil {
		r.tcp.Close()
	}
}

// startKDEConnect brings up the gated KDE Connect adapter, reusing the same
// consent prompt and save pipeline as every other receive path. Returns a stop
// func, or nil if it could not bind (port busy). Only compiled with the tag.
func (a *App) startKDEConnect(dest string) func() {
	name := a.deviceName
	if name == "" {
		name = "krokodyl"
	}
	id := a.machineID
	if id == "" {
		id = "krokodyl"
	}
	r, err := newKDEConnectReceiver(dest, id, name, kcDefaultPort, a.localSendOffer, func(fname string, size int64) {
		a.tm.add(FileTransfer{
			ID:       "receive-kde-" + uuid.NewString(),
			Name:     fname,
			Files:    []string{fname},
			Size:     size,
			Status:   FileTransferStatusCompleted,
			Progress: 100,
		})
	})
	if err != nil {
		logrus.WithError(err).Info("KDE Connect receiving unavailable")
		return nil
	}
	return r.close
}
