//go:build krokodyl_warpinator

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"krokodyl/warpinator/warppb"
)

// stubSender is a minimal Warpinator SENDER: it serves StartTransfer and
// streams one file's chunks. Used to drive our real receiver end-to-end.
type stubSender struct {
	warppb.UnimplementedWarpServer
	relPath string
	data    []byte
}

func (s *stubSender) StartTransfer(_ *warppb.OpInfo, stream warppb.Warp_StartTransferServer) error {
	// Two chunks, to exercise multi-chunk assembly.
	mid := len(s.data) / 2
	for _, part := range [][]byte{s.data[:mid], s.data[mid:]} {
		if err := stream.Send(&warppb.FileChunk{RelativePath: s.relPath, Chunk: part}); err != nil {
			return err
		}
	}
	return nil
}

// End-to-end interop over real gRPC: a stub sender + our REAL warpReceiver.
// Drives ProcessTransferOpRequest → consent → receiver pulls StartTransfer →
// chunks assembled + saved. Proves the transfer path works without the actual
// Warpinator app.
//
// Run: go test -tags krokodyl_warpinator -race -run TestWarpinatorEndToEnd
func TestWarpinatorEndToEnd(t *testing.T) {
	dir := t.TempDir()
	const payload = "warpinator chunked payload bytes"

	// Stand up the stub sender on its own gRPC port.
	senderLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	senderSrv := grpc.NewServer()
	warppb.RegisterWarpServer(senderSrv, &stubSender{relPath: "doc.txt", data: []byte(payload)})
	go senderSrv.Serve(senderLn)
	defer senderSrv.Stop()
	senderAddr := senderLn.Addr().String()

	// Our real receiver, with the dialer pointed at the stub sender.
	got := make(chan string, 1)
	r, err := newWarpReceiver(dir, "krokodyl", 0,
		func(string, string, []string, int64) bool { return true },
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
	r.dial = func(string) (warppb.WarpClient, func() error, error) {
		conn, err := grpc.NewClient(senderAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, err
		}
		return warppb.NewWarpClient(conn), conn.Close, nil
	}

	// Drive the receiver as a sender would: call its ProcessTransferOpRequest.
	conn, err := grpc.NewClient("127.0.0.1:"+itoa(r.port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := warppb.NewWarpClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = client.ProcessTransferOpRequest(ctx, &warppb.TransferOpRequest{
		Info:         &warppb.OpInfo{Ident: "op-1"},
		SenderName:   "Linux Box",
		Size:         uint64(len(payload)),
		Count:        1,
		NameIfSingle: "doc.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case name := <-got:
		if name != "doc.txt" {
			t.Errorf("saved name = %q", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("receiver never pulled + saved the file")
	}
	data, err := os.ReadFile(filepath.Join(dir, "doc.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != payload {
		t.Errorf("content = %q, want %q", data, payload)
	}
}

// A declined offer must not pull or write anything.
func TestWarpinatorDeclineNoPull(t *testing.T) {
	dir := t.TempDir()
	r, err := newWarpReceiver(dir, "krokodyl", 0,
		func(string, string, []string, int64) bool { return false }, // decline
		func(string, int64) { t.Error("must not save when declined") })
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	r.dial = func(string) (warppb.WarpClient, func() error, error) {
		t.Error("must not dial the sender when declined")
		return nil, nil, context.Canceled
	}

	conn, _ := grpc.NewClient("127.0.0.1:"+itoa(r.port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	client := warppb.NewWarpClient(conn)
	client.ProcessTransferOpRequest(context.Background(), &warppb.TransferOpRequest{
		Info: &warppb.OpInfo{Ident: "op-2"}, SenderName: "x", Count: 1, NameIfSingle: "y.txt",
	})
	time.Sleep(300 * time.Millisecond)
}
