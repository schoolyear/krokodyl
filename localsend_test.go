package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestLocalSend(t *testing.T, accept bool) (*localSendReceiver, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	var got []string
	r := &localSendReceiver{
		dest:     dir,
		self:     lsDeviceInfo{Alias: "krokodyl", Version: localSendVersion, DeviceType: "desktop", Fingerprint: "selffp", Port: localSendPort, Protocol: "http"},
		onOffer:  func(string, string, []string, int64) bool { return accept },
		onFile:   func(name string, size int64) { got = append(got, name) },
		sessions: make(map[string]*lsSession),
		stopCh:   make(chan struct{}),
	}
	return r, dir, &got
}

func prepareBody(files map[string]lsFileMeta) *bytes.Buffer {
	b, _ := json.Marshal(lsPrepareRequest{
		Info:  lsDeviceInfo{Alias: "Pixel", Fingerprint: "peerfp"},
		Files: files,
	})
	return bytes.NewBuffer(b)
}

func TestLocalSendInfo(t *testing.T) {
	r, _, _ := newTestLocalSend(t, true)
	rec := httptest.NewRecorder()
	r.handleInfo(rec, httptest.NewRequest(http.MethodGet, "/api/localsend/v2/info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("info = %d", rec.Code)
	}
	var info lsDeviceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Alias != "krokodyl" || info.Version != localSendVersion || info.DeviceType != "desktop" {
		t.Errorf("bad info: %+v", info)
	}
}

func TestLocalSendPrepareUploadAcceptIssuesTokens(t *testing.T) {
	r, _, _ := newTestLocalSend(t, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/localsend/v2/prepare-upload",
		prepareBody(map[string]lsFileMeta{"f1": {ID: "f1", FileName: "a.txt", Size: 3}}))
	r.handlePrepareUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("prepare = %d (%s)", rec.Code, rec.Body.String())
	}
	var resp lsPrepareResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.SessionID == "" || resp.Files["f1"] == "" {
		t.Errorf("expected session + token, got %+v", resp)
	}
	if _, ok := r.sessions[resp.SessionID]; !ok {
		t.Error("session not stored")
	}
}

func TestLocalSendPrepareUploadRejected(t *testing.T) {
	r, _, _ := newTestLocalSend(t, false) // user declines
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/localsend/v2/prepare-upload",
		prepareBody(map[string]lsFileMeta{"f1": {ID: "f1", FileName: "a.txt", Size: 3}}))
	r.handlePrepareUpload(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("declined prepare = %d, want 403", rec.Code)
	}
	if len(r.sessions) != 0 {
		t.Error("no session should be created on reject")
	}
}

func TestLocalSendUploadFlow(t *testing.T) {
	r, dir, got := newTestLocalSend(t, true)

	// prepare
	rec := httptest.NewRecorder()
	r.handlePrepareUpload(rec, httptest.NewRequest(http.MethodPost, "/x",
		prepareBody(map[string]lsFileMeta{"f1": {ID: "f1", FileName: "report.pdf", Size: 5}})))
	var resp lsPrepareResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	token := resp.Files["f1"]

	// upload with correct token
	up := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/localsend/v2/upload?sessionId="+resp.SessionID+"&fileId=f1&token="+token,
		strings.NewReader("hello"))
	r.handleUpload(up, req)
	if up.Code != http.StatusOK {
		t.Fatalf("upload = %d (%s)", up.Code, up.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.pdf"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("file not saved correctly: %v %q", err, data)
	}
	if len(*got) != 1 || (*got)[0] != "report.pdf" {
		t.Errorf("onFile not fired correctly: %v", *got)
	}
	// token consumed → session gone
	if len(r.sessions) != 0 {
		t.Error("session should be cleared after last file")
	}
}

func TestLocalSendUploadRejectsBadToken(t *testing.T) {
	r, _, _ := newTestLocalSend(t, true)
	rec := httptest.NewRecorder()
	r.handlePrepareUpload(rec, httptest.NewRequest(http.MethodPost, "/x",
		prepareBody(map[string]lsFileMeta{"f1": {ID: "f1", FileName: "a.txt", Size: 1}})))
	var resp lsPrepareResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	// wrong token
	up := httptest.NewRecorder()
	r.handleUpload(up, httptest.NewRequest(http.MethodPost,
		"/api/localsend/v2/upload?sessionId="+resp.SessionID+"&fileId=f1&token=WRONG",
		strings.NewReader("x")))
	if up.Code != http.StatusForbidden {
		t.Errorf("bad token = %d, want 403", up.Code)
	}
	// unknown session
	up2 := httptest.NewRecorder()
	r.handleUpload(up2, httptest.NewRequest(http.MethodPost,
		"/api/localsend/v2/upload?sessionId=nope&fileId=f1&token=x", strings.NewReader("x")))
	if up2.Code != http.StatusForbidden {
		t.Errorf("unknown session = %d, want 403", up2.Code)
	}
}

func TestLocalSendUploadTraversalContained(t *testing.T) {
	r, dir, _ := newTestLocalSend(t, true)
	rec := httptest.NewRecorder()
	r.handlePrepareUpload(rec, httptest.NewRequest(http.MethodPost, "/x",
		prepareBody(map[string]lsFileMeta{"f1": {ID: "f1", FileName: "../../escape.txt", Size: 1}})))
	var resp lsPrepareResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	up := httptest.NewRecorder()
	r.handleUpload(up, httptest.NewRequest(http.MethodPost,
		"/api/localsend/v2/upload?sessionId="+resp.SessionID+"&fileId=f1&token="+resp.Files["f1"],
		strings.NewReader("x")))
	if up.Code != http.StatusOK {
		t.Fatalf("upload = %d", up.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err != nil {
		t.Errorf("file should be contained as escape.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Error("file escaped destination")
	}
}
