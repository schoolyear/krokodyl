package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
)

// Phones can't be made to appear in krokodyl via Apple AirDrop or Android
// Quick Share — those are closed, app-only protocols (see
// .claude/spikes/mobile-native-share-feasibility.md). The portable way a phone
// sends to a desktop, used by every cross-platform tool, is a local web
// upload: krokodyl runs a tiny token-gated HTTP server while the user opts in,
// the phone scans a QR, and a browser upload page POSTs files straight into the
// normal staging/destination pipeline. Works from any phone with no app, over
// the shared Wi-Fi or the offline hotspot.

const (
	uploadFieldName = "files"
	// tokenHeader carries the gate token on the POST so it never lands in a URL
	// (and thus not in browser history or a Referer header). The GET page URL
	// still carries ?t= because the QR must open it; the POST uses the header.
	tokenHeader = "X-Krokodyl-Token"
	// readHeaderTimeout stops a slow-loris client from pinning a connection
	// before its headers are even read (the token check can't run until then).
	// The body itself is NOT time-capped — large uploads need to stream freely.
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 60 * time.Second
	// maxUploadBytes is a generous per-request ceiling so a token holder can't
	// fill the disk without bound, while still allowing very large transfers.
	maxUploadBytes = 50 << 30 // 50 GiB
	// maxUploadNameLen bounds a sanitized filename so it can't exceed common
	// filesystem limits (most cap a path component at 255 bytes).
	maxUploadNameLen = 200
)

// webReceiver owns the opt-in upload server. It is only listening between
// StartPhoneReceive and StopPhoneReceive. Fields are set once at construction
// and never mutated, so no lock is needed.
type webReceiver struct {
	srv   *http.Server
	token string
	dest  string
	port  int
	// onFile is called after each file is fully written, with the actual name
	// it was saved under, so the app can record a transfer and notify the UI.
	onFile func(name string, size int64)
}

// newWebReceiver binds an ephemeral port on all interfaces (the phone must be
// able to reach it) and starts serving. A 128-bit token gates every request so
// another device on the LAN cannot upload without the QR.
func newWebReceiver(dest string, onFile func(string, int64)) (*webReceiver, error) {
	if onFile == nil {
		onFile = func(string, int64) {}
	}
	info, err := os.Stat(dest)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("destination is not a usable directory: %s", dest)
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return nil, fmt.Errorf("could not generate upload token: %w", err)
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("could not open upload port: %w", err)
	}

	wr := &webReceiver{
		token:  hex.EncodeToString(tok),
		dest:   dest,
		port:   ln.Addr().(*net.TCPAddr).Port,
		onFile: onFile,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", wr.handleIndex)
	mux.HandleFunc("/upload", wr.handleUpload)
	wr.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		if err := wr.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.WithError(err).Warn("phone-receive server stopped")
		}
	}()
	return wr, nil
}

// checkToken constant-time-compares the request token (header preferred, query
// fallback for the GET page) against ours.
func (wr *webReceiver) checkToken(r *http.Request) bool {
	got := r.Header.Get(tokenHeader)
	if got == "" {
		got = r.URL.Query().Get("t")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(wr.token)) == 1
}

func (wr *webReceiver) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !wr.checkToken(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Keep the token-bearing URL out of the browser cache.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, uploadPageHTML, wr.token)
}

func (wr *webReceiver) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !wr.checkToken(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected multipart upload", http.StatusBadRequest)
		return
	}

	saved := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			http.Error(w, "malformed upload", http.StatusBadRequest)
			return
		}
		if part.FormName() != uploadFieldName || part.FileName() == "" {
			part.Close()
			continue // skip non-file fields
		}
		name, n, err := wr.savePart(part.FileName(), part)
		part.Close()
		if err != nil {
			logrus.WithError(err).Warn("phone-receive: could not save uploaded file")
			http.Error(w, "could not save file", http.StatusInternalServerError)
			return
		}
		saved++
		wr.onFile(name, n)
	}
	if saved == 0 {
		http.Error(w, "no files in upload", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "received %d file(s)", saved)
}

func (wr *webReceiver) savePart(rawName string, src io.Reader) (string, int64, error) {
	return saveUploadedFile(wr.dest, rawName, src)
}

// saveUploadedFile streams one uploaded file into dest under a sanitized,
// collision-free name (returned), via a tmp file then rename so a half-written
// upload never appears at the final name. Shared by the web-upload and
// LocalSend receivers — both take untrusted filenames off the network.
func saveUploadedFile(dest, rawName string, src io.Reader) (string, int64, error) {
	name, err := safeUploadName(dest, rawName)
	if err != nil {
		return "", 0, fmt.Errorf("save: choose name for %q: %w", rawName, err)
	}
	final := filepath.Join(dest, name)
	tmp := final + ".krokodyl-part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("save: create %q: %w", name, err)
	}
	n, err := io.Copy(out, src)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		return "", 0, fmt.Errorf("save: write %q: %w", name, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", 0, fmt.Errorf("save: close %q: %w", name, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", 0, fmt.Errorf("save: finalize %q: %w", name, err)
	}
	return name, n, nil
}

func (wr *webReceiver) close() {
	if wr.srv != nil {
		_ = wr.srv.Close()
	}
}

// safeUploadName turns an untrusted uploaded filename into a safe, unique name
// inside dest: strips any path, sanitizes display chars, length-caps, rejects
// traversal, and de-duplicates with a " (n)" suffix so an upload never
// clobbers an existing file.
func safeUploadName(dest, raw string) (string, error) {
	base := filepath.Base(filepath.FromSlash(raw))
	base = strings.TrimSpace(sanitizeDisplayName(base))
	if base == "" || base == "." || base == ".." {
		base = "received-file"
	}
	base = clampUploadName(base)
	if err := validateRelPath(base); err != nil {
		return "", err
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	name := base
	for i := 1; ; i++ {
		_, err := os.Stat(filepath.Join(dest, name))
		if errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			// A non-"not exists" stat error (permissions, I/O) must not spin
			// the loop forever — surface it.
			return "", fmt.Errorf("stat %q: %w", name, err)
		}
		name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
	}
}

// clampUploadName bounds a filename's length while preserving its extension.
func clampUploadName(name string) string {
	if len(name) <= maxUploadNameLen {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) >= maxUploadNameLen {
		return name[:maxUploadNameLen]
	}
	return name[:maxUploadNameLen-len(ext)] + ext
}

// phoneReceiveURL builds the address the phone opens: the best real-LAN IP
// (real LANs ranked above virtual adapters) plus port and token.
func phoneReceiveURL(port int, token string) string {
	host := "127.0.0.1"
	if ips := localUnicastIPs(); len(ips) > 0 {
		host = ips[0]
	}
	return fmt.Sprintf("http://%s:%d/?t=%s", host, port, token)
}

// PhoneReceiveInfo is handed to the frontend so it can show the QR + URL.
type PhoneReceiveInfo struct {
	URL   string `json:"url"`
	QRPng string `json:"qrPng"` // data: URL of a PNG QR code, "" if generation failed
}

// StartPhoneReceive opens the opt-in upload server and returns the QR/URL the
// phone scans. Files arrive in the default download directory and appear as
// completed transfers. Calling again restarts cleanly with a fresh token.
func (a *App) StartPhoneReceive() (PhoneReceiveInfo, error) {
	dest, err := a.GetDefaultDownloadPath()
	if err != nil {
		return PhoneReceiveInfo{}, err
	}

	wr, err := newWebReceiver(dest, func(name string, size int64) {
		a.tm.add(FileTransfer{
			ID:       "receive-web-" + uuid.NewString(),
			Name:     name,
			Files:    []string{name},
			Size:     size,
			Status:   FileTransferStatusCompleted,
			Progress: 100,
		})
	})
	if err != nil {
		return PhoneReceiveInfo{}, err
	}

	// Atomic swap: store the new receiver and close any previous one outside
	// the lock, so two concurrent starts can't leak an untracked server.
	a.mu.Lock()
	old := a.webRecv
	a.webRecv = wr
	oldLS := a.localSend
	a.localSend = nil
	oldKDE := a.kdeStop
	a.kdeStop = nil
	oldWarp := a.warpStop
	a.warpStop = nil
	a.mu.Unlock()
	// Close any previous receivers BEFORE starting new ones — KDE Connect and
	// Warpinator bind fixed ports, so a lingering old listener blocks the new.
	if old != nil {
		old.close()
	}
	if oldLS != nil {
		oldLS.close()
	}
	if oldKDE != nil {
		oldKDE()
	}
	if oldWarp != nil {
		oldWarp()
	}

	// Also become discoverable to LocalSend apps (best-effort).
	a.startLocalSend(dest)
	// And to KDE Connect / Warpinator, if built with their tags (no-ops
	// otherwise). Stored so they're stopped with the rest.
	if stop := a.startKDEConnect(dest); stop != nil {
		a.mu.Lock()
		a.kdeStop = stop
		a.mu.Unlock()
	}
	if stop := a.startWarpinator(dest); stop != nil {
		a.mu.Lock()
		a.warpStop = stop
		a.mu.Unlock()
	}

	url := phoneReceiveURL(wr.port, wr.token)
	info := PhoneReceiveInfo{URL: url}
	if png, err := qrcode.Encode(url, qrcode.Medium, 320); err == nil {
		info.QRPng = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	} else {
		logrus.WithError(err).Debug("phone-receive: QR generation failed")
	}
	return info, nil
}

// StopPhoneReceive shuts the upload + LocalSend servers down (also called on
// app shutdown).
func (a *App) StopPhoneReceive() {
	a.mu.Lock()
	wr := a.webRecv
	a.webRecv = nil
	ls := a.localSend
	a.localSend = nil
	kdeStop := a.kdeStop
	a.kdeStop = nil
	warpStop := a.warpStop
	a.warpStop = nil
	a.mu.Unlock()
	if wr != nil {
		wr.close()
	}
	if ls != nil {
		ls.close()
	}
	if kdeStop != nil {
		kdeStop()
	}
	if warpStop != nil {
		warpStop()
	}
}

const uploadPageHTML = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Send to krokodyl</title>
<style>
 body{font-family:system-ui,sans-serif;margin:0;padding:1.5rem;background:#101315;color:#E9EDF0}
 h1{font-size:1.25rem}
 .card{max-width:30rem;margin:0 auto}
 input[type=file]{display:block;width:100%%;margin:1rem 0;padding:1rem;background:#181C1F;border:1px dashed #323B43;border-radius:.5rem;color:#E9EDF0}
 button{width:100%%;padding:.9rem;font-size:1rem;font-weight:700;border:0;border-radius:.5rem;background:#0E8050;color:#fff}
 #status{margin-top:1rem;font-weight:700}
</style></head>
<body><div class="card">
 <h1>🐊 Send to krokodyl</h1>
 <form id="f" method="post" enctype="multipart/form-data">
   <input type="file" name="files" multiple required>
   <button type="submit">Send</button>
 </form>
 <div id="status" role="status" aria-live="polite"></div>
 <script>
  var TOKEN='%[1]s';
  var f=document.getElementById('f'),s=document.getElementById('status');
  f.addEventListener('submit',function(e){
    e.preventDefault();s.textContent='Sending…';
    fetch('/upload',{method:'POST',headers:{'X-Krokodyl-Token':TOKEN},body:new FormData(f)})
      .then(function(r){return r.ok?r.text():Promise.reject(r.status)})
      .then(function(t){s.textContent='✅ '+t})
      .catch(function(){s.textContent='❌ Upload failed'});
  });
 </script>
</div></body></html>`
