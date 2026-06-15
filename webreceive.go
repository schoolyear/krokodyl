package main

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
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
	// tokenHeader carries the gate token on the POST so the upload REQUEST need
	// not put it in a URL (kept out of any Referer / access log). The QR page URL
	// still carries ?t= because the QR must open the page; the POST prefers the
	// header. The token is over HTTPS either way.
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
	// maxConcurrentUploads caps simultaneous upload streams. The server is
	// always-on while visible and bound on all interfaces, so even a valid token
	// holder must not be able to open unbounded 50 GiB streams at once and
	// exhaust disk/memory. A normal phone POSTs all files in one request.
	maxConcurrentUploads = 8
)

// webReceiver owns the opt-in upload server. It is only listening while the
// device is visible. Fields are set once at construction and never mutated, so
// no lock is needed.
type webReceiver struct {
	srv   *http.Server
	token string
	dest  string
	port  int
	// sem bounds concurrent upload streams (buffered to maxConcurrentUploads).
	sem chan struct{}
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
	tok, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("could not generate upload token: %w", err)
	}
	// HTTPS only — uploads and the token must never cross the wire in the
	// clear. Self-signed (no CA), so the phone browser shows a one-time
	// "not private" warning; the connection is still encrypted.
	cert, err := ephemeralCertificate()
	if err != nil {
		return nil, fmt.Errorf("could not create upload certificate: %w", err)
	}
	ln, err := tls.Listen("tcp", ":0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("could not open upload port: %w", err)
	}

	wr := &webReceiver{
		token:  tok,
		dest:   dest,
		port:   ln.Addr().(*net.TCPAddr).Port,
		sem:    make(chan struct{}, maxConcurrentUploads),
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

// ctEqual is a constant-time string comparison (timing-safe token check).
func ctEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// checkToken matches the request's token, header preferred (the upload POST),
// query fallback (the GET page the QR opens). The page is reloadable so a phone
// browser — including the cert-warning interstitial flow on a self-signed
// HTTPS host — can re-fetch it without re-scanning.
func (wr *webReceiver) checkToken(r *http.Request) bool {
	got := r.Header.Get(tokenHeader)
	if got == "" {
		got = r.URL.Query().Get("t")
	}
	return ctEqual(got, wr.token)
}

func (wr *webReceiver) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !wr.checkToken(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Keep the token-bearing page URL out of caches and Referer headers.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
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
	// Bound concurrent streams: only token-authenticated requests reach here, so
	// a slot is never consumed by an unauthenticated caller. Saturated => 429
	// rather than block, so a flood can't pin connections open indefinitely.
	select {
	case wr.sem <- struct{}{}:
		defer func() { <-wr.sem }()
	default:
		http.Error(w, "too many concurrent uploads", http.StatusTooManyRequests)
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

// randomToken returns a 128-bit cryptographically-random hex string, used for
// the bootstrap, upload, and session credentials.
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// phoneReceiveURL builds the address the phone opens: the best real-LAN IP
// (real LANs ranked above virtual adapters) plus port and bootstrap token.
func phoneReceiveURL(port int, token string) string {
	host := "127.0.0.1"
	if ips := localUnicastIPs(); len(ips) > 0 {
		host = ips[0]
	}
	return fmt.Sprintf("https://%s:%d/?t=%s", host, port, token)
}

// PhoneReceiveInfo is handed to the frontend so it can show the QR + URL.
type PhoneReceiveInfo struct {
	URL   string `json:"url"`
	QRPng string `json:"qrPng"` // data: URL of a PNG QR code, "" if generation failed
}

// StartPhoneReceive opens the opt-in upload server and returns the QR/URL the
// phone scans. Files arrive in the default download directory and appear as
// completed transfers. Calling again restarts cleanly with a fresh token.
// ensureReceiving makes krokodyl reachable from phones/LocalSend/etc. It is
// idempotent and runs whenever the device is visible (started at launch, not
// behind a manual toggle) so LocalSend & co. always find krokodyl out of the
// box. Best-effort: failures are logged, never fatal.
func (a *App) ensureReceiving(dest string) {
	a.mu.Lock()
	already := a.webRecv != nil
	a.mu.Unlock()
	if already {
		return
	}

	wr, err := newWebReceiver(dest, func(name string, size int64) {
		a.tm.add(FileTransfer{
			ID:       "receive-web-" + uuid.NewString(),
			Name:     name,
			Files:    []string{name},
			Size:     size,
			Status:   FileTransferStatusCompleted,
			Progress: 100,
			Path:     filepath.Join(dest, name),
		})
	})
	if err != nil {
		logrus.WithError(err).Info("phone web-upload unavailable")
		return
	}
	a.mu.Lock()
	if a.webRecv != nil { // lost a race; keep the existing one
		a.mu.Unlock()
		wr.close()
		return
	}
	a.webRecv = wr
	a.mu.Unlock()

	// Always discoverable to LocalSend apps (best-effort), and to KDE Connect /
	// Warpinator when built with their tags (no-ops otherwise).
	a.startLocalSend(dest)
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
}

// stopReceiving tears down every receive server (visibility off / shutdown).
func (a *App) stopReceiving() {
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

// StartPhoneReceive ensures the receive servers are up and returns the QR/URL
// for browser uploads. Receiving is already on by default while visible; this
// just surfaces the QR on demand.
func (a *App) StartPhoneReceive() (PhoneReceiveInfo, error) {
	dest, err := a.GetDefaultDownloadPath()
	if err != nil {
		return PhoneReceiveInfo{}, err
	}
	a.ensureReceiving(dest)

	a.mu.Lock()
	wr := a.webRecv
	a.mu.Unlock()
	if wr == nil {
		return PhoneReceiveInfo{}, fmt.Errorf("phone receiving is unavailable")
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

// StopPhoneReceive is retained for the frontend binding but receiving is now
// tied to nearby visibility (always on while visible), so closing the QR view
// no longer stops it. Kept as an explicit override if ever needed.
func (a *App) StopPhoneReceive() { a.stopReceiving() }

// uploadPageHTML is a self-contained, mobile-first upload page (no external
// assets — it is served over the LAN on a self-signed cert). %[1]s is the
// upload token, embedded for the X-Krokodyl-Token header on the POST. All
// literal percent signs are doubled for fmt.
const uploadPageHTML = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="color-scheme" content="dark">
<title>Send to krokodyl</title>
<style>
 :root{--bg:#0F1213;--surface:#181C1F;--surface2:#212629;--border:#323B43;--text:#E9EDF0;--dim:#9AA7AE;--accent:#0E8050;--accent-h:#0B6B43;--accent-t:#2FBF8F}
 *{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
 html,body{margin:0}
 body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;background:var(--bg);color:var(--text);min-height:100dvh;display:flex;flex-direction:column;padding:max(1rem,env(safe-area-inset-top)) max(1rem,env(safe-area-inset-right)) max(1rem,env(safe-area-inset-bottom)) max(1rem,env(safe-area-inset-left))}
 .wrap{width:100%%;max-width:32rem;margin:0 auto;flex:1;display:flex;flex-direction:column;gap:1.1rem}
 header{display:flex;align-items:center;gap:.65rem;padding:.4rem 0 .2rem}
 .logo{font-size:1.7rem;line-height:1}
 h1{font-size:1.15rem;margin:0;font-weight:700;letter-spacing:-.01em}
 .sub{margin:.12rem 0 0;font-size:.78rem;color:var(--dim)}
 .drop{flex:1;min-height:38vh;border:2px dashed var(--border);border-radius:18px;background:var(--surface);display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.55rem;text-align:center;padding:1.5rem;cursor:pointer;transition:border-color .15s,background .15s}
 .drop.over,.drop:active{border-color:var(--accent-t);background:var(--surface2)}
 .drop .ico{font-size:2.6rem;line-height:1}
 .drop .big{font-weight:700;font-size:1.05rem}
 .drop .small{font-size:.8rem;color:var(--dim)}
 input[type=file]{position:absolute;width:1px;height:1px;opacity:0;pointer-events:none}
 .files{display:flex;flex-direction:column;gap:.5rem}
 .file{display:flex;align-items:center;gap:.65rem;background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:.6rem .7rem}
 .file .nm{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:.9rem}
 .file .sz{font-size:.74rem;color:var(--dim);flex-shrink:0}
 .file .rm{background:none;border:0;color:var(--dim);font-size:1.15rem;line-height:1;padding:.3rem .45rem;cursor:pointer;flex-shrink:0;min-height:36px;min-width:36px}
 .foot{position:sticky;bottom:0;display:flex;flex-direction:column;gap:.55rem;padding-top:.3rem;background:linear-gradient(to top,var(--bg) 70%%,transparent)}
 .bar{height:8px;background:var(--surface2);border-radius:99px;overflow:hidden;display:none}
 .bar>i{display:block;height:100%%;width:0;background:var(--accent-t);transition:width .12s}
 .status{text-align:center;font-weight:600;font-size:.88rem;min-height:1.15rem}
 .status.ok{color:var(--accent-t)}
 .status.err{color:#ff6b5e}
 .file.done{border-color:var(--accent-t)}
 .file .check{color:var(--accent-t);font-weight:700;flex-shrink:0}
 .file .badge{color:var(--accent-t);font-size:.72rem;font-weight:700;flex-shrink:0}
 .send{width:100%%;padding:1rem;font-size:1.05rem;font-weight:700;border:0;border-radius:14px;background:var(--accent);color:#fff;cursor:pointer;min-height:54px}
 .send:active:not(:disabled){background:var(--accent-h)}
 .send:disabled{opacity:.45;cursor:default}
</style></head>
<body>
 <div class="wrap">
  <header>
   <span class="logo" aria-hidden="true">🐊</span>
   <div><h1>Send to krokodyl</h1><p class="sub">Files arrive in the computer's downloads.</p></div>
  </header>
  <div class="files" id="done"></div>
  <label class="drop" id="drop" for="pick">
   <span class="ico" aria-hidden="true">📤</span>
   <span class="big">Tap to choose files</span>
   <span class="small">or drag &amp; drop them here</span>
  </label>
  <input id="pick" type="file" multiple>
  <div class="files" id="list"></div>
  <div class="foot">
   <div class="bar" id="bar"><i id="fill"></i></div>
   <div class="status" id="status" role="status" aria-live="polite"></div>
   <button class="send" id="send" disabled>Send</button>
  </div>
 </div>
 <script>
  var TOKEN='%[1]s';
  var pick=document.getElementById('pick'),drop=document.getElementById('drop'),list=document.getElementById('list'),send=document.getElementById('send'),status=document.getElementById('status'),bar=document.getElementById('bar'),fill=document.getElementById('fill'),done=document.getElementById('done');
  var files=[],completed=[];
  function sz(n){if(n<1024)return n+' B';if(n<1048576)return (n/1024).toFixed(0)+' KB';if(n<1073741824)return (n/1048576).toFixed(1)+' MB';return (n/1073741824).toFixed(2)+' GB';}
  function renderDone(){
   done.innerHTML='';
   completed.forEach(function(f){
    var r=document.createElement('div');r.className='file done';
    var c=document.createElement('span');c.className='check';c.textContent='✓';
    var n=document.createElement('span');n.className='nm';n.textContent=f.name;
    var s=document.createElement('span');s.className='badge';s.textContent='Completed';
    r.appendChild(c);r.appendChild(n);r.appendChild(s);done.appendChild(r);
   });
  }
  function render(){
   list.innerHTML='';
   files.forEach(function(f,i){
    var r=document.createElement('div');r.className='file';
    var n=document.createElement('span');n.className='nm';n.textContent=f.name;
    var s=document.createElement('span');s.className='sz';s.textContent=sz(f.size);
    var b=document.createElement('button');b.className='rm';b.textContent='✕';b.setAttribute('aria-label','Remove '+f.name);
    b.onclick=function(e){e.preventDefault();files.splice(i,1);render();};
    r.appendChild(n);r.appendChild(s);r.appendChild(b);list.appendChild(r);
   });
   send.disabled=files.length===0;
   send.textContent=files.length?('Send '+files.length+' file'+(files.length>1?'s':'')):'Send';
  }
  pick.addEventListener('change',function(){for(var i=0;i<pick.files.length;i++)files.push(pick.files[i]);pick.value='';render();});
  ['dragenter','dragover'].forEach(function(ev){drop.addEventListener(ev,function(e){e.preventDefault();drop.classList.add('over');});});
  ['dragleave','dragend','drop'].forEach(function(ev){drop.addEventListener(ev,function(e){e.preventDefault();drop.classList.remove('over');});});
  drop.addEventListener('drop',function(e){var dt=e.dataTransfer;if(dt&&dt.files)for(var i=0;i<dt.files.length;i++)files.push(dt.files[i]);render();});
  send.addEventListener('click',function(){
   if(!files.length)return;
   var fd=new FormData();files.forEach(function(f){fd.append('files',f,f.name);});
   var x=new XMLHttpRequest();x.open('POST','/upload');x.setRequestHeader('X-Krokodyl-Token',TOKEN);
   send.disabled=true;bar.style.display='block';fill.style.width='0';status.className='status';status.textContent='Sending…';
   x.upload.onprogress=function(e){if(e.lengthComputable)fill.style.width=(e.loaded/e.total*100)+'%%';};
   x.onload=function(){
    bar.style.display='none';fill.style.width='0';
    if(x.status===200){files.forEach(function(f){completed.push({name:f.name});});files=[];render();renderDone();status.className='status ok';status.textContent='✅ Sent — choose more to send again';}
    else{status.className='status err';status.textContent='❌ Failed ('+x.status+')';send.disabled=false;}
   };
   x.onerror=function(){bar.style.display='none';status.className='status err';status.textContent='❌ Connection lost';send.disabled=false;};
   x.send(fd);
  });
 </script>
</body></html>`
