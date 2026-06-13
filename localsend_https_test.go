package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Proves the LocalSend receiver serves over real HTTPS and that fingerprint
// pinning is enforced: a client pinning the announced fingerprint succeeds, a
// client pinning the wrong one is rejected. (Runs on an OS-assigned port so it
// never collides with a real instance on 53317.)
func TestLocalSendHTTPSAndPinning(t *testing.T) {
	r, err := newLocalSendReceiver(t.TempDir(), "Warm Ocelot", 0,
		func(string, string, []string, int64) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	if r.self.Protocol != "https" {
		t.Errorf("must announce https, got %q", r.self.Protocol)
	}
	if len(r.self.Fingerprint) != 64 {
		t.Errorf("fingerprint should be 64 hex chars (SHA-256), got %d", len(r.self.Fingerprint))
	}

	infoURL := "https://127.0.0.1:" + strconv.Itoa(r.port) + "/api/localsend/v2/info"

	// Correct pin → succeeds over TLS.
	good := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: pinFingerprint(r.self.Fingerprint),
	}}}
	resp, err := good.Get(infoURL)
	if err != nil {
		t.Fatalf("pinned HTTPS GET failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Warm Ocelot") {
		t.Errorf("info response wrong: %d %s", resp.StatusCode, body)
	}

	// Wrong pin → TLS verification must reject the connection.
	bad := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: pinFingerprint(strings.Repeat("ab", 32)),
	}}}
	if _, err := bad.Get(infoURL); err == nil {
		t.Error("wrong fingerprint must be rejected, but the request succeeded")
	}

	// Plain HTTP to the TLS port must NOT serve the API (the TLS server replies
	// 400 to a plaintext request rather than our info JSON).
	if resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(r.port) + "/api/localsend/v2/info"); err == nil {
		if resp.StatusCode == http.StatusOK {
			t.Error("plain HTTP must not serve the info endpoint on the HTTPS listener")
		}
		resp.Body.Close()
	}
}

func TestPinFingerprintEmptySkips(t *testing.T) {
	// An empty expected fingerprint (unknown peer) skips pinning rather than
	// breaking discovery.
	if err := pinFingerprint("")([][]byte{{0x01}}, nil); err != nil {
		t.Errorf("empty expected should skip, got %v", err)
	}
}
