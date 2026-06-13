package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestReceiver(t *testing.T) (*webReceiver, string) {
	t.Helper()
	dir := t.TempDir()
	var got []string
	wr := &webReceiver{
		token: "secret-token",
		dest:  dir,
		onFile: func(name string, size int64) {
			got = append(got, name)
		},
	}
	return wr, dir
}

func TestSafeUploadName(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		raw  string
		want string
	}{
		{"photo.jpg", "photo.jpg"},
		{"../../etc/passwd", "passwd"}, // path stripped
		{`..\..\windows\system32\x.dll`, "x.dll"},
		{"", "received-file"},
		{"..", "received-file"},
		{"a/b/c.txt", "c.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := safeUploadName(dir, tt.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("safeUploadName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSafeUploadNameDedups(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, "doc (1).pdf"), []byte("x"), 0o600)

	got, err := safeUploadName(dir, "doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if got != "doc (2).pdf" {
		t.Errorf("dedup = %q, want %q", got, "doc (2).pdf")
	}
}

func uploadRequest(t *testing.T, token, field, filename, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte(content))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/upload?t="+token, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestWebReceiverRejectsBadToken(t *testing.T) {
	wr, _ := newTestReceiver(t)

	// GET index without token.
	rec := httptest.NewRecorder()
	wr.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("index without token = %d, want 403", rec.Code)
	}
	// POST upload with wrong token.
	rec = httptest.NewRecorder()
	wr.handleUpload(rec, uploadRequest(t, "wrong", "files", "a.txt", "hi"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("upload with wrong token = %d, want 403", rec.Code)
	}
}

func TestWebReceiverAcceptsUpload(t *testing.T) {
	wr, dir := newTestReceiver(t)
	rec := httptest.NewRecorder()
	wr.handleUpload(rec, uploadRequest(t, "secret-token", "files", "hello.txt", "hello phone"))

	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("uploaded file not written: %v", err)
	}
	if string(data) != "hello phone" {
		t.Errorf("content = %q, want %q", data, "hello phone")
	}
}

func TestWebReceiverUploadTraversalContained(t *testing.T) {
	wr, dir := newTestReceiver(t)
	rec := httptest.NewRecorder()
	wr.handleUpload(rec, uploadRequest(t, "secret-token", "files", "../../escape.txt", "nope"))

	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d (%s)", rec.Code, rec.Body.String())
	}
	// Must land inside dest as escape.txt, never in a parent.
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err != nil {
		t.Errorf("file should be contained in dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Error("file escaped the destination directory")
	}
}

func TestWebReceiverRejectsGet(t *testing.T) {
	wr, _ := newTestReceiver(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/upload?t=secret-token", nil)
	wr.handleUpload(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET upload = %d, want 405", rec.Code)
	}
}

// End-to-end: a real server via newWebReceiver, a real HTTP upload carrying
// the token in the header, file written + onFile fired, clean shutdown.
func TestWebReceiverEndToEnd(t *testing.T) {
	dir := t.TempDir()
	gotCh := make(chan string, 1)
	wr, err := newWebReceiver(dir, func(name string, size int64) {
		select {
		case gotCh <- name:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer wr.close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("files", "from-phone.txt")
	fw.Write([]byte("hello from the phone"))
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		"http://127.0.0.1:"+itoa(wr.port)+"/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(tokenHeader, wr.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "from-phone.txt")); err != nil {
		t.Errorf("file not written: %v", err)
	}
	select {
	case name := <-gotCh:
		if name != "from-phone.txt" {
			t.Errorf("onFile name = %q", name)
		}
	default:
		t.Error("onFile not called")
	}

	// A request with no token must be rejected by the real server too.
	noTok, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+itoa(wr.port)+"/", nil)
	r2, err := http.DefaultClient.Do(noTok)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusForbidden {
		t.Errorf("index without token = %d, want 403", r2.StatusCode)
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func TestClampUploadName(t *testing.T) {
	long := strings.Repeat("a", 300) + ".txt"
	got := clampUploadName(long)
	if len(got) > maxUploadNameLen {
		t.Errorf("clamped len %d exceeds %d", len(got), maxUploadNameLen)
	}
	if !strings.HasSuffix(got, ".txt") {
		t.Errorf("clamp dropped the extension: %q", got)
	}
}

func TestPhoneReceiveURL(t *testing.T) {
	url := phoneReceiveURL(53201, "tok123")
	if !strings.HasPrefix(url, "http://") || !strings.Contains(url, ":53201/?t=tok123") {
		t.Errorf("unexpected URL: %q", url)
	}
}
