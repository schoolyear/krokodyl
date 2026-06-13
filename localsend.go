package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
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

	// onOffer asks the user to accept an incoming send (alias, sender IP, file
	// names, total size); blocks until answered. Reuses the nearby prompt.
	onOffer func(alias, addr string, files []string, size int64) bool
	onFile  func(name string, size int64)

	mu       sync.Mutex
	sessions map[string]*lsSession

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

// newLocalSendReceiver binds the LocalSend port and starts the HTTP API +
// multicast announce/listen. A bind failure (e.g. another LocalSend instance)
// is non-fatal: it returns an error the caller logs, and the rest of receiving
// (the QR web upload) still works.
func newLocalSendReceiver(dest, alias string, onOffer func(string, string, []string, int64) bool, onFile func(string, int64)) (*localSendReceiver, error) {
	if onOffer == nil {
		onOffer = func(string, string, []string, int64) bool { return false }
	}
	if onFile == nil {
		onFile = func(string, int64) {}
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", localSendPort))
	if err != nil {
		return nil, fmt.Errorf("localsend port %d unavailable: %w", localSendPort, err)
	}

	r := &localSendReceiver{
		dest: dest,
		self: lsDeviceInfo{
			Alias:       alias,
			Version:     localSendVersion,
			DeviceType:  "desktop",
			Fingerprint: randomFingerprint(),
			Port:        localSendPort,
			Protocol:    "http",
			Download:    false,
		},
		onOffer:  onOffer,
		onFile:   onFile,
		sessions: make(map[string]*lsSession),
		stopCh:   make(chan struct{}),
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
		if err := r.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
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

func (r *localSendReceiver) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, r.self)
}

func (r *localSendReceiver) handleRegister(w http.ResponseWriter, _ *http.Request) {
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
		sess.names[id] = f.FileName
		resp.Files[id] = tok
	}
	r.mu.Lock()
	r.sessions[resp.SessionID] = sess
	r.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

func (r *localSendReceiver) handleUpload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := req.URL.Query()
	sessionID, fileID, token := q.Get("sessionId"), q.Get("fileId"), q.Get("token")

	r.mu.Lock()
	sess := r.sessions[sessionID]
	r.mu.Unlock()
	if sess == nil {
		http.Error(w, "unknown session", http.StatusForbidden)
		return
	}
	want, ok := sess.tokens[fileID]
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, localSendMaxUploadBytes)
	name, n, err := saveUploadedFile(r.dest, sess.names[fileID], req.Body)
	if err != nil {
		logrus.WithError(err).Warn("localsend: could not save uploaded file")
		http.Error(w, "could not save file", http.StatusInternalServerError)
		return
	}
	// One token per file: consume it so a token can't be replayed.
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
// device announce, registers back over HTTP so discovery is reliable even if a
// UDP reply is lost.
func (r *localSendReceiver) runMulticast() {
	gaddr := &net.UDPAddr{IP: net.ParseIP(localSendMulticastAddr), Port: localSendPort}

	// Listener.
	conn, err := net.ListenMulticastUDP("udp4", nil, gaddr)
	if err != nil {
		logrus.WithError(err).Debug("localsend multicast listen unavailable")
	} else {
		go r.readAnnouncements(conn)
		go func() { <-r.stopCh; conn.Close() }()
	}

	// Announcer.
	out, err := net.DialUDP("udp4", nil, gaddr)
	if err != nil {
		logrus.WithError(err).Debug("localsend multicast announce unavailable")
		return
	}
	defer out.Close()
	announce := r.self
	announce.Announce = true
	payload, _ := json.Marshal(announce)

	ticker := time.NewTicker(localSendAnnounceEvery)
	defer ticker.Stop()
	for {
		_, _ = out.Write(payload)
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func (r *localSendReceiver) readAnnouncements(conn *net.UDPConn) {
	buf := make([]byte, 4096)
	for {
		n, src, err := conn.ReadFromUDP(buf)
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
		go r.registerWith(src.IP.String(), info.Port)
	}
}

// startLocalSend brings up LocalSend interop for the opt-in receive window.
// Best-effort: a bind failure (port busy / another LocalSend running) is
// logged and skipped — the QR web-upload path still works.
func (a *App) startLocalSend(dest string) {
	alias := a.deviceName
	if alias == "" {
		alias = "krokodyl"
	}
	ls, err := newLocalSendReceiver(dest, alias, a.localSendOffer, func(name string, size int64) {
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

// registerWith tells a freshly-discovered peer about us over HTTP.
func (r *localSendReceiver) registerWith(host string, port int) {
	if port <= 0 || port > 65535 {
		return
	}
	body := r.self
	body.Announce = false
	data, _ := json.Marshal(body)
	url := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/api/localsend/v2/register"
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	resp.Body.Close()
}
