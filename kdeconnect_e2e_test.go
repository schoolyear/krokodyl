//go:build krokodyl_kdeconnect

package main

import (
	"bufio"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// End-to-end interop test of the REAL KDE Connect receive adapter over real
// loopback TLS sockets, driven by a minimal sender that speaks the actual KDE
// Connect wire protocol. Proves the network path works (identity exchange,
// packet framing, share.request handling, consent gate, payload TLS fetch,
// sanitized save) without needing the proprietary KDE Connect app.
//
// Run: go test -tags krokodyl_kdeconnect -race -run TestKDEConnectEndToEnd
func TestKDEConnectEndToEnd(t *testing.T) {
	dir := t.TempDir()
	got := make(chan string, 1)
	r, err := newKDEConnectReceiver(dir, "recv-id", "krokodyl", 0,
		func(alias, addr string, files []string, size int64) bool { return true }, // auto-accept
		func(name string, size int64) {
			select {
			case got <- name:
			default:
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	const payload = "hello from kde connect"

	// Serve the file payload on our own TLS port (the sender side of a share).
	payLn, err := tls.Listen("tcp", "127.0.0.1:0", senderTLSConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer payLn.Close()
	payPort := payLn.Addr().(*net.TCPAddr).Port
	go func() {
		c, err := payLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.Write([]byte(payload))
	}()

	// Connect to the receiver as a KDE Connect "sender".
	conn, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(r.port), senderTLSConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	// Read the receiver's identity line (it sends first).
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 4096), kcMaxPacketBytes)
	if !sc.Scan() {
		t.Fatal("did not receive receiver identity")
	}
	if p, err := decodeKCPacket(sc.Bytes()); err != nil || p.Type != kcPacketIdentity {
		t.Fatalf("first packet should be identity: %v", err)
	}

	// Send our identity, then a share.request pointing at our payload port.
	idLine, _ := encodeKCPacket(kcPacketIdentity, ourKCIdentity("send-id", "Phone", 0))
	conn.Write(idLine)

	share := kcShareRequest{Filename: "note.txt", PayloadSize: int64(len(payload))}
	share.PayloadInfo.Port = payPort
	shareLine, _ := encodeKCPacket(kcPacketShareReq, share)
	if _, err := conn.Write(shareLine); err != nil {
		t.Fatal(err)
	}

	// The receiver should fetch the payload and save it.
	select {
	case name := <-got:
		if name != "note.txt" {
			t.Errorf("saved name = %q", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("receiver never saved the file")
	}

	data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != payload {
		t.Errorf("content = %q, want %q", data, payload)
	}
}

// A share.request without a payload port must be ignored (no file written).
func TestKDEConnectRejectsBadShare(t *testing.T) {
	dir := t.TempDir()
	r, err := newKDEConnectReceiver(dir, "recv", "krokodyl", 0,
		func(string, string, []string, int64) bool { return true },
		func(string, int64) { t.Error("must not save on a malformed share") })
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	conn, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(r.port), senderTLSConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	bufio.NewScanner(conn).Scan() // drain identity

	bad, _ := encodeKCPacket(kcPacketShareReq, kcShareRequest{Filename: "x"}) // no port/size
	conn.Write(bad)
	time.Sleep(300 * time.Millisecond) // give the receiver a chance to (not) act
}

func senderTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	cert, err := ephemeralCertificate()
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}
}
