package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/ipv4"
)

// LocalSend interop: krokodyl speaks the open LocalSend v2 protocol so the
// LocalSend app (iOS/Android/desktop) discovers it and sends files to it — no
// krokodyl phone app, no closed AirDrop/Quick Share. This is the RECEIVE side:
// multicast announce so LocalSend finds us, then the v2 HTTP API
// (info/register/prepare-upload/upload/cancel). Incoming transfers require the
// same human accept as nearby offers (reusing the nearby:offer prompt), then
// stream into the downloads folder via the shared sanitized save path.
//
// Runs only while the user has "Receive from a phone" on (one opt-in, one
// lifecycle), so the LocalSend port isn't open in the background.

const (
	localSendMulticastAddr = "224.0.0.167"
	localSendPort          = 53317
	localSendVersion       = "2.0"
	localSendAnnounceEvery = 3 * time.Second
	// Per-file token the sender must echo on upload (we issue it at
	// prepare-upload, after the user accepts).
	localSendMaxUploadBytes = maxUploadBytes
)

type lsDeviceInfo struct {
	Alias       string  `json:"alias"`
	Version     string  `json:"version"`
	DeviceModel *string `json:"deviceModel"`
	DeviceType  string  `json:"deviceType"`
	Fingerprint string  `json:"fingerprint"`
	Port        int     `json:"port"`
	Protocol    string  `json:"protocol"`
	Download    bool    `json:"download"`
	Announce    bool    `json:"announce,omitempty"`
}

type lsFileMeta struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	FileType string `json:"fileType"`
}

type lsPrepareRequest struct {
	Info  lsDeviceInfo          `json:"info"`
	Files map[string]lsFileMeta `json:"files"`
}

type lsPrepareResponse struct {
	SessionID string            `json:"sessionId"`
	Files     map[string]string `json:"files"` // fileId -> token
}

// lsSession is an accepted prepare-upload awaiting its file bytes.
type lsSession struct {
	tokens map[string]string // fileId -> token
	names  map[string]string // fileId -> sanitized-source fileName
}

// localSendReceiver serves the LocalSend v2 API and announces presence.
type localSendReceiver struct {
	srv  *http.Server
	dest string
	self lsDeviceInfo
	port int // actual bound TLS port (exposed for tests)

	// onOffer asks the user to accept an incoming send (alias, sender IP, file
	// names, total size); blocks until answered. Reuses the nearby prompt.
	onOffer func(alias, addr string, files []string, size int64) bool
	onFile  func(name string, size int64)

	mu       sync.Mutex
	sessions map[string]*lsSession

	// offerBusy enforces one pending accept prompt at a time, so a hostile LAN
	// device can't spam prompts / pile up blocked goroutines.
	offerBusy atomic.Bool
	// registerSem bounds concurrent register-back POSTs against a multicast
	// flood (each uses a per-peer pinned TLS client, so no shared client).
	registerSem chan struct{}

	stopOnce sync.Once
	stopCh   chan struct{}
}

func randomFingerprint() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "krokodyl"
	}
	return hex.EncodeToString(b)
}

// newLocalSendReceiver binds the LocalSend port over TLS and starts the HTTPS
// API + multicast announce/listen. We announce protocol "https" with the
// SHA-256 fingerprint of our self-signed cert; peers pin that fingerprint
// (LocalSend's trust model — same as krokodyl's own nearby channel). A bind
// failure is non-fatal: the caller logs it and the QR web upload still works.
func newLocalSendReceiver(dest, alias string, port int, onOffer func(string, string, []string, int64) bool, onFile func(string, int64)) (*localSendReceiver, error) {
	if onOffer == nil {
		onOffer = func(string, string, []string, int64) bool { return false }
	}
	if onFile == nil {
		onFile = func(string, int64) {}
	}
	cert, err := ephemeralCertificate()
	if err != nil {
		return nil, fmt.Errorf("localsend: certificate: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", port), tlsCfg)
	if err != nil && port == localSendPort {
		// The standard port is busy — most likely the official LocalSend app is
		// already running on this machine. Take an ephemeral port instead and
		// announce it; LocalSend peers connect to the port from the announcement,
		// so we coexist with the app rather than vanishing.
		logrus.WithError(err).Info("localsend: port 53317 busy, using an ephemeral port and announcing it")
		ln, err = tls.Listen("tcp", ":0", tlsCfg)
	}
	if err != nil {
		return nil, fmt.Errorf("localsend port %d unavailable: %w", port, err)
	}
	bound := ln.Addr().(*net.TCPAddr).Port

	r := &localSendReceiver{
		dest: dest,
		port: bound,
		self: lsDeviceInfo{
			Alias:       alias,
			Version:     localSendVersion,
			DeviceType:  "desktop",
			Fingerprint: certFingerprint(cert.Certificate[0]),
			Port:        bound,
			Protocol:    "https",
			Download:    false,
		},
		onOffer:     onOffer,
		onFile:      onFile,
		sessions:    make(map[string]*lsSession),
		registerSem: make(chan struct{}, 8),
		stopCh:      make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/info", r.handleInfo)
	mux.HandleFunc("/api/localsend/v2/register", r.handleRegister)
	mux.HandleFunc("/api/localsend/v2/prepare-upload", r.handlePrepareUpload)
	mux.HandleFunc("/api/localsend/v2/upload", r.handleUpload)
	mux.HandleFunc("/api/localsend/v2/cancel", r.handleCancel)
	r.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		if err := r.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.WithError(err).Warn("localsend server stopped")
		}
	}()
	go r.runMulticast()
	return r, nil
}

func (r *localSendReceiver) close() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	if r.srv != nil {
		_ = r.srv.Close()
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (r *localSendReceiver) handleInfo(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, r.self)
}

func (r *localSendReceiver) handleRegister(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// We don't need the peer's body to function; reply with our own info so the
	// peer can reach us. (Bodies are bounded by ReadHeaderTimeout + the default
	// server limits.)
	writeJSON(w, http.StatusOK, r.self)
}

func (r *localSendReceiver) handlePrepareUpload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var pr lsPrepareRequest
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&pr); err != nil {
		http.Error(w, "invalid prepare-upload", http.StatusBadRequest)
		return
	}
	if len(pr.Files) == 0 || len(pr.Files) > maxOfferFiles {
		http.Error(w, "invalid file set", http.StatusBadRequest)
		return
	}

	// One pending accept prompt at a time (matches the nearby control channel)
	// — a flood of prepare-uploads can't spam prompts or pile up blocked
	// goroutines.
	if !r.offerBusy.CompareAndSwap(false, true) {
		http.Error(w, "busy", http.StatusTooManyRequests)
		return
	}
	defer r.offerBusy.Store(false)

	names := make([]string, 0, len(pr.Files))
	var total int64
	for _, f := range pr.Files {
		names = append(names, sanitizeDisplayName(f.FileName))
		if f.Size > 0 {
			total += f.Size
		}
	}

	addr := req.RemoteAddr
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		addr = host
	}
	alias := sanitizeDisplayName(pr.Info.Alias)
	if alias == "" {
		alias = "LocalSend device"
	}

	// Human accept (reuses the nearby offer prompt). Blocks until answered.
	if !r.onOffer(alias, addr, names, total) {
		http.Error(w, "rejected", http.StatusForbidden)
		return
	}

	sess := &lsSession{tokens: make(map[string]string), names: make(map[string]string)}
	resp := lsPrepareResponse{SessionID: uuid.NewString(), Files: make(map[string]string)}
	for id, f := range pr.Files {
		tok := randomFingerprint()
		sess.tokens[id] = tok
		// Store the sanitized name so what was shown to the user is what gets
		// saved (saveUploadedFile re-validates regardless).
		sess.names[id] = sanitizeDisplayName(f.FileName)
		resp.Files[id] = tok
	}
	r.mu.Lock()
	r.sessions[resp.SessionID] = sess
	r.mu.Unlock()
	r.expireSession(resp.SessionID)

	writeJSON(w, http.StatusOK, resp)
}

// expireSession drops an accepted session after a window if its uploads never
// arrive, so abandoned sessions don't accumulate for the receiver's lifetime.
func (r *localSendReceiver) expireSession(id string) {
	go func() {
		select {
		case <-time.After(5 * time.Minute):
		case <-r.stopCh:
		}
		r.mu.Lock()
		delete(r.sessions, id)
		r.mu.Unlock()
	}()
}

func (r *localSendReceiver) handleUpload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := req.URL.Query()
	sessionID, fileID, token := q.Get("sessionId"), q.Get("fileId"), q.Get("token")

	// Copy what we need out under the lock; sess maps are shared across
	// concurrent uploads in the same session, so never touch them unlocked.
	r.mu.Lock()
	sess := r.sessions[sessionID]
	var want, rawName string
	var known bool
	if sess != nil {
		want, known = sess.tokens[fileID]
		rawName = sess.names[fileID]
	}
	r.mu.Unlock()

	if sess == nil {
		http.Error(w, "unknown session", http.StatusForbidden)
		return
	}
	if !known || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, localSendMaxUploadBytes)
	name, n, err := saveUploadedFile(r.dest, rawName, req.Body)
	if err != nil {
		logrus.WithError(err).Warn("localsend: could not save uploaded file")
		http.Error(w, "could not save file", http.StatusInternalServerError)
		return
	}
	// One token per file: consume it under the lock so it can't be replayed.
	r.mu.Lock()
	delete(sess.tokens, fileID)
	if len(sess.tokens) == 0 {
		delete(r.sessions, sessionID)
	}
	r.mu.Unlock()

	r.onFile(name, n)
	w.WriteHeader(http.StatusOK)
}

func (r *localSendReceiver) handleCancel(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("sessionId")
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// runMulticast announces our presence periodically and, when it hears another
// device announce, registers back over HTTPS so discovery is reliable even if a
// UDP reply is lost.
//
// CRITICAL on multi-adapter Windows: a host with Hyper-V / WSL / NAT virtual
// switches has several "up" interfaces, and the OS-default multicast interface
// is frequently one of those virtual switches — NOT the real Wi-Fi the phone is
// on. We therefore announce on, and join the group on, EVERY real interface.
//
// RECEIVE on Windows requires binding 0.0.0.0:port and JoinGroup via the socket
// option. net.ListenMulticastUDP binds the GROUP ADDRESS, which on Windows SENDS
// fine but RECEIVES NOTHING (verified on a real box: not even loopback). That
// bug made krokodyl deaf to the phone's announce, so it never registered back
// and the phone never saw it. The official LocalSend app binds 0.0.0.0, hence it
// worked where we didn't.
func (r *localSendReceiver) runMulticast() {
	gaddr := &net.UDPAddr{IP: net.ParseIP(localSendMulticastAddr), Port: localSendPort}
	ifaces := multicastInterfaces()

	// Listen: one socket bound to 0.0.0.0:port (SO_REUSEADDR so we coexist with
	// the official LocalSend app), joining the group on each real interface.
	lc := net.ListenConfig{Control: controlReuseAddr}
	if pktConn, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", localSendPort)); err != nil {
		logrus.WithError(err).Debug("localsend multicast listen unavailable")
	} else {
		rpc := ipv4.NewPacketConn(pktConn)
		_ = rpc.SetMulticastLoopback(true)
		joined := 0
		for i := range ifaces {
			if err := rpc.JoinGroup(&ifaces[i], gaddr); err != nil {
				logrus.WithError(err).WithField("iface", ifaces[i].Name).Debug("localsend: JoinGroup failed")
				continue
			}
			joined++
		}
		if joined == 0 {
			_ = rpc.JoinGroup(nil, gaddr) // OS-default interface
		}
		go r.readAnnouncements(rpc)
		go func() { <-r.stopCh; pktConn.Close() }()
	}

	// Announce: one socket, explicitly setting the multicast EGRESS interface
	// before each send. This is the correct cross-platform way — binding a local
	// address does NOT reliably set IP_MULTICAST_IF on Windows, so without this
	// the announce leaves via the OS-default (often virtual) interface and the
	// phone never hears us, even though our join side already hears the phone.
	announce := r.self
	announce.Announce = true
	payload, _ := json.Marshal(announce)

	uconn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		logrus.WithError(err).Debug("localsend multicast announce unavailable")
		return
	}
	defer uconn.Close()
	go func() { <-r.stopCh; uconn.Close() }()
	pc := ipv4.NewPacketConn(uconn)

	send := func() {
		if len(ifaces) == 0 {
			_, _ = pc.WriteTo(payload, nil, gaddr) // OS default
			return
		}
		for i := range ifaces {
			if err := pc.SetMulticastInterface(&ifaces[i]); err != nil {
				logrus.WithError(err).WithField("iface", ifaces[i].Name).Debug("localsend: set multicast iface failed")
				continue
			}
			_, _ = pc.WriteTo(payload, nil, gaddr)
		}
	}

	ticker := time.NewTicker(localSendAnnounceEvery)
	defer ticker.Stop()
	for {
		send()
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
		}
	}
}

// multicastInterfaces returns up, multicast-capable, non-loopback interfaces
// that have a usable IPv4 address, ordered real-LAN-first (192.168/10 before
// the 172.16/12 ranges that Hyper-V/Docker virtual switches use). We must
// explicitly use every real one because the OS default multicast interface on
// a multi-adapter Windows box is often a virtual switch.
func multicastInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	type ranked struct {
		iface net.Interface
		rank  int
	}
	var got []ranked
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if ip := firstIPv4(iface); ip != nil {
			got = append(got, ranked{iface, addrRank(ip.String())})
		}
	}
	sort.SliceStable(got, func(i, j int) bool { return got[i].rank < got[j].rank })
	out := make([]net.Interface, len(got))
	for i := range got {
		out[i] = got[i].iface
	}
	return out
}

// firstIPv4 returns the first non-loopback, non-link-local IPv4 address bound
// to iface, or nil.
func firstIPv4(iface net.Interface) net.IP {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if ip4 := ipnet.IP.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
				return ip4
			}
		}
	}
	return nil
}

func (r *localSendReceiver) readAnnouncements(pc *ipv4.PacketConn) {
	buf := make([]byte, 4096)
	for {
		n, _, src, err := pc.ReadFrom(buf)
		if err != nil {
			return // closed
		}
		var info lsDeviceInfo
		if err := json.Unmarshal(buf[:n], &info); err != nil || !info.Announce {
			continue
		}
		if info.Fingerprint == r.self.Fingerprint {
			continue // our own announcement
		}
		host := ""
		if ua, ok := src.(*net.UDPAddr); ok {
			host = ua.IP.String()
		}
		// Bound concurrent register-backs so a multicast flood can't spawn
		// unbounded goroutines / outbound POSTs; drop when saturated.
		select {
		case r.registerSem <- struct{}{}:
			go func(host string, port int, fp string) {
				defer func() { <-r.registerSem }()
				r.registerWith(host, port, fp)
			}(host, info.Port, info.Fingerprint)
		default:
		}
	}
}

// controlReuseAddr is a net.ListenConfig hook that sets SO_REUSEADDR on the
// socket before bind, so krokodyl's multicast listener can share the port with
// the official LocalSend app's listener.
func controlReuseAddr(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) { serr = setReuseAddr(fd) }); err != nil {
		return err
	}
	return serr
}

// startLocalSend brings up LocalSend interop for the opt-in receive window.
// Best-effort: a bind failure (port busy / another LocalSend running) is
// logged and skipped — the QR web-upload path still works.
func (a *App) startLocalSend(dest string) {
	alias := a.deviceName
	if alias == "" {
		alias = "krokodyl"
	}
	ls, err := newLocalSendReceiver(dest, alias, localSendPort, a.localSendOffer, func(name string, size int64) {
		a.tm.add(FileTransfer{
			ID:       "receive-ls-" + uuid.NewString(),
			Name:     name,
			Files:    []string{name},
			Size:     size,
			Status:   FileTransferStatusCompleted,
			Progress: 100,
		})
	})
	if err != nil {
		logrus.WithError(err).Info("LocalSend receiving unavailable")
		return
	}
	a.mu.Lock()
	a.localSend = ls
	a.mu.Unlock()
}

// localSendOffer surfaces an incoming LocalSend transfer through the existing
// nearby-offer prompt and blocks until the user answers (or it times out).
func (a *App) localSendOffer(alias, addr string, files []string, size int64) bool {
	id := "ls-offer-" + uuid.NewString()
	ch := make(chan bool, 1)
	a.mu.Lock()
	a.lsOffers[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.lsOffers, id)
		a.mu.Unlock()
	}()

	a.emitEvent(NearbyEventOffer, NearbyOffer{
		ID: id, SenderName: alias, SenderAddr: addr, Files: files, Size: size,
	})

	t := time.NewTimer(offerPromptWait)
	defer t.Stop()
	select {
	case ok := <-ch:
		return ok
	case <-t.C:
		return false
	}
}

// resolveLocalSendOffer delivers the user's answer to a pending LocalSend
// offer. Unknown ids (a croc nearby offer, or already-answered) are no-ops.
func (a *App) resolveLocalSendOffer(offerID string, accept bool) {
	a.mu.Lock()
	ch, ok := a.lsOffers[offerID]
	if ok {
		delete(a.lsOffers, offerID)
	}
	a.mu.Unlock()
	if ok {
		select {
		case ch <- accept:
		default:
		}
	}
}

// registerWith tells a freshly-discovered peer about us over HTTPS, pinning the
// peer's announced certificate fingerprint. The target is restricted to a
// private-LAN address so a spoofed announcement can't turn register-back into
// an arbitrary-host probe (SSRF) — LocalSend lives on the local network.
func (r *localSendReceiver) registerWith(host string, port int, fingerprint string) {
	if port <= 0 || port > 65535 {
		return
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsPrivate() {
		return
	}
	// No announced fingerprint => no way to authenticate the peer's cert, so we
	// will not open even a register-back to it. A conformant LocalSend v2 device
	// always announces one over HTTPS; an empty field is non-conformant/suspect.
	if fingerprint == "" {
		return
	}
	body := r.self
	body.Announce = false
	data, _ := json.Marshal(body)

	// Per-peer pinned client (fingerprints differ per peer, so no shared one);
	// bounded by registerSem at the call site.
	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:            tls.VersionTLS12,
			InsecureSkipVerify:    true, //nolint:gosec // self-signed; pinned below
			VerifyPeerCertificate: pinFingerprint(fingerprint),
		}},
	}
	url := "https://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/api/localsend/v2/register"
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	resp.Body.Close()
}

// pinFingerprint verifies a presented self-signed cert against the SHA-256
// fingerprint the peer announced (LocalSend's identity check). It FAILS CLOSED:
// an empty expected fingerprint is treated as "no identity to pin against" and
// rejected, so this primitive can never silently yield an unauthenticated TLS
// connection. Callers that have no fingerprint must not dial at all.
func pinFingerprint(expected string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if expected == "" {
			return fmt.Errorf("no expected fingerprint to pin against")
		}
		if len(rawCerts) == 0 {
			return fmt.Errorf("peer presented no certificate")
		}
		if subtle.ConstantTimeCompare([]byte(certFingerprint(rawCerts[0])), []byte(expected)) != 1 {
			return fmt.Errorf("peer certificate does not match announced fingerprint")
		}
		return nil
	}
}
