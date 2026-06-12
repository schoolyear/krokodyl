package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Zero-code sends negotiate over a direct TLS control connection between two
// instances on the LAN: the sender offers (name, files, size), the receiver's
// user accepts or declines, and only after an accept does the internally
// generated croc code travel back over the same encrypted connection. The
// transfer itself then runs through the existing worker engine.
//
// The TLS certificate is ephemeral and self-signed, but NOT blindly trusted:
// every instance announces the SHA-256 fingerprint of its certificate in its
// discovery payload, and the dialer verifies the presented certificate
// against the fingerprint of the specific peer it means to contact. The
// control channel is thereby pinned to the announced identity — an attacker
// must compromise discovery and the TLS handshake together, and the
// mandatory accept prompt (name + address) remains the human backstop.

const (
	// Receiver auto-declines an unanswered offer after this long; the
	// sender's overall connection deadline is a bit larger so the decline
	// always wins the race.
	offerPromptTimeout = 60 * time.Second
	offerDialTimeout   = 75 * time.Second

	maxOfferWireBytes = 64 * 1024
	maxOfferFiles     = 1000
	maxFileNameLen    = 512
	maxCodeLen        = 256

	// Cap on concurrently handled control connections: prevents a hostile
	// LAN peer from parking thousands of goroutines on the 75s deadline.
	maxConcurrentOfferConns = 16
)

type offerRequest struct {
	SenderName string   `json:"senderName"`
	Files      []string `json:"files"`
	Size       int64    `json:"size"`
}

type offerAnswer struct {
	Accepted bool `json:"accepted"`
	Busy     bool `json:"busy,omitempty"`
}

type codeMessage struct {
	Code string `json:"code"`
}

// NearbyOffer is what the receiving frontend renders in the accept prompt.
type NearbyOffer struct {
	ID         string   `json:"id"`
	SenderName string   `json:"senderName"`
	SenderAddr string   `json:"senderAddr"`
	Files      []string `json:"files"`
	Size       int64    `json:"size"`
}

func validateOfferRequest(req offerRequest) error {
	if req.SenderName == "" || len(req.SenderName) > maxPeerNameLen {
		return fmt.Errorf("invalid sender name")
	}
	if len(req.Files) == 0 || len(req.Files) > maxOfferFiles {
		return fmt.Errorf("invalid file count %d", len(req.Files))
	}
	for _, f := range req.Files {
		if len(f) > maxFileNameLen {
			return fmt.Errorf("file name too long")
		}
	}
	if req.Size < 0 {
		return fmt.Errorf("invalid size")
	}
	return nil
}

// nearbyServer answers incoming offers. One pending offer at a time: humans
// answer one prompt at a time, and a busy answer is honest.
type nearbyServer struct {
	listener net.Listener
	sem      chan struct{}

	mu        sync.Mutex
	pendingID string
	pendingCh chan bool

	onOffer  func(NearbyOffer)
	onAccept func(senderName, code string)
}

// startNearbyServer listens for offers on a dynamic port. Callbacks: onOffer
// surfaces the prompt; onAccept fires after the code handoff and starts the
// actual receive. The returned fingerprint is announced via discovery so
// dialers can pin this server's certificate.
func startNearbyServer(onOffer func(NearbyOffer), onAccept func(senderName, code string)) (srv *nearbyServer, port int, fingerprint string, err error) {
	cert, err := ephemeralCertificate()
	if err != nil {
		return nil, 0, "", fmt.Errorf("could not create control-channel certificate: %w", err)
	}
	fingerprint = certFingerprint(cert.Certificate[0])

	listener, err := tls.Listen("tcp", ":0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		return nil, 0, "", fmt.Errorf("could not open control channel: %w", err)
	}

	srv = &nearbyServer{
		listener: listener,
		sem:      make(chan struct{}, maxConcurrentOfferConns),
		onOffer:  onOffer,
		onAccept: onAccept,
	}
	go srv.serve()

	port = listener.Addr().(*net.TCPAddr).Port
	return srv, port, fingerprint, nil
}

// certFingerprint is the hex SHA-256 of the certificate's DER bytes.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func (s *nearbyServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.handle(conn)
			}()
		default:
			conn.Close() // shed load: too many concurrent control connections
		}
	}
}

func (s *nearbyServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(offerDialTimeout))

	dec := json.NewDecoder(io.LimitReader(conn, maxOfferWireBytes))
	enc := json.NewEncoder(conn)

	var req offerRequest
	if err := dec.Decode(&req); err != nil {
		logrus.WithError(err).Debug("ignoring malformed nearby offer")
		return
	}
	if err := validateOfferRequest(req); err != nil {
		logrus.WithError(err).Debug("rejecting invalid nearby offer")
		_ = enc.Encode(offerAnswer{Accepted: false})
		return
	}

	offerID := uuid.NewString()
	ch := make(chan bool, 1)

	s.mu.Lock()
	if s.pendingCh != nil {
		s.mu.Unlock()
		_ = enc.Encode(offerAnswer{Accepted: false, Busy: true})
		return
	}
	s.pendingID = offerID
	s.pendingCh = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.pendingID == offerID {
			s.pendingID = ""
			s.pendingCh = nil
		}
		s.mu.Unlock()
	}()

	senderAddr := ""
	if host, _, err := net.SplitHostPort(conn.RemoteAddr().String()); err == nil {
		senderAddr = host
	}
	s.onOffer(NearbyOffer{
		ID:         offerID,
		SenderName: req.SenderName,
		SenderAddr: senderAddr,
		Files:      req.Files,
		Size:       req.Size,
	})

	accepted := false
	promptTimer := time.NewTimer(offerPromptTimeout)
	select {
	case accepted = <-ch:
	case <-promptTimer.C:
	}
	promptTimer.Stop()

	if err := enc.Encode(offerAnswer{Accepted: accepted}); err != nil {
		return
	}
	if !accepted {
		return
	}

	var code codeMessage
	if err := dec.Decode(&code); err != nil || code.Code == "" || len(code.Code) > maxCodeLen {
		logrus.WithError(err).Warn("nearby offer accepted but a valid code never arrived")
		return
	}
	s.onAccept(req.SenderName, code.Code)
}

// respond resolves the pending offer prompt. Unknown ids are no-ops (the
// prompt may have timed out already).
func (s *nearbyServer) respond(offerID string, accept bool) {
	s.mu.Lock()
	ch := s.pendingCh
	match := s.pendingID == offerID
	s.mu.Unlock()

	if match && ch != nil {
		select {
		case ch <- accept:
		default:
		}
	}
}

func (s *nearbyServer) close() {
	_ = s.listener.Close()
	// Unblock a pending prompt as a decline.
	s.mu.Lock()
	ch := s.pendingCh
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- false:
		default:
		}
	}
}

// perCandidateDialTimeout bounds each address attempt so trying several
// candidates (a multi-homed peer advertises all its IPs) stays quick.
const perCandidateDialTimeout = 4 * time.Second

// sendNearbyOffer tries each candidate address until one connects, then
// presents the offer over that connection. A multi-homed peer (Hyper-V / WSL
// / Docker / VPN) advertises several addresses; only some are routable from
// here, so dialing just one would fail spuriously. Once any address connects,
// its outcome (accept/decline/busy) is returned — we do not keep trying.
func sendNearbyOffer(candidates []string, port int, expectedFingerprint string, req offerRequest, code string) (offerAnswer, error) {
	if expectedFingerprint == "" {
		return offerAnswer{}, fmt.Errorf("device announced no certificate fingerprint")
	}
	if len(candidates) == 0 {
		return offerAnswer{}, fmt.Errorf("device advertised no reachable address")
	}

	var lastErr error
	for _, addr := range candidates {
		answer, connected, err := offerToAddress(addr, port, expectedFingerprint, req, code)
		if connected {
			return answer, err
		}
		logrus.WithError(err).Debugf("nearby offer could not reach %s", addr)
		lastErr = err
	}
	return offerAnswer{}, fmt.Errorf("could not reach device on any address (tried %v): %w", candidates, lastErr)
}

// offerToAddress attempts a single address. connected reports whether the TLS
// connection was established; when true the caller stops (the peer was
// reached) and uses answer/err as the final result.
func offerToAddress(addr string, port int, expectedFingerprint string, req offerRequest, code string) (answer offerAnswer, connected bool, err error) {
	dialer := &net.Dialer{Timeout: perCandidateDialTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(addr, fmt.Sprint(port)), &tls.Config{
		// Self-signed ephemeral certs have no CA chain or hostname to
		// check; identity comes from pinning the certificate announced in
		// the discovery payload instead.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("peer presented no certificate")
			}
			got := certFingerprint(rawCerts[0])
			if subtle.ConstantTimeCompare([]byte(got), []byte(expectedFingerprint)) != 1 {
				return fmt.Errorf("peer certificate does not match its announced fingerprint")
			}
			return nil
		},
	})
	if err != nil {
		// Could not establish the connection on this address — caller tries
		// the next candidate.
		return offerAnswer{}, false, err
	}
	defer conn.Close()
	logrus.Debugf("nearby offer connected via %s", addr)
	_ = conn.SetDeadline(time.Now().Add(offerDialTimeout))

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(io.LimitReader(conn, maxOfferWireBytes))

	if err := enc.Encode(req); err != nil {
		return offerAnswer{}, true, fmt.Errorf("could not send offer: %w", err)
	}

	if err := dec.Decode(&answer); err != nil {
		return offerAnswer{}, true, fmt.Errorf("no answer from device: %w", err)
	}
	if !answer.Accepted {
		return answer, true, nil
	}

	if err := enc.Encode(codeMessage{Code: code}); err != nil {
		return offerAnswer{}, true, fmt.Errorf("could not hand over transfer code: %w", err)
	}
	return answer, true, nil
}

func ephemeralCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "krokodyl-nearby"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}
