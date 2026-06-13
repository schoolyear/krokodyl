//go:build krokodyl_warpinator

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"

	"krokodyl/warpinator/warppb"
)

// Warpinator receive adapter, compiled only with `-tags krokodyl_warpinator`.
//
// The transfer logic is exercised end-to-end over real gRPC by
// warpinator_e2e_test.go (our server + a stub sender). What is NOT validated
// here is interop with the REAL Warpinator app, which additionally requires
// zeroconf discovery and the group-code TLS cert exchange — this adapter dials
// the sender over an insecure channel (auth/cert-pinning is the remaining work,
// see .claude/spikes/warpinator-runbook.md). Gated off until that lands.

const warpBuildEnabled = true

type warpReceiver struct {
	warppb.UnimplementedWarpServer
	dest    string
	info    warpRemoteMachineInfo
	onOffer func(alias, addr string, files []string, size int64) bool
	onFile  func(name string, size int64)
	// dial connects to the sender to pull file chunks; injectable for tests.
	dial func(peerAddr string) (warppb.WarpClient, func() error, error)

	grpc *grpc.Server
	port int
}

func newWarpReceiver(dest, deviceName string, port int, onOffer func(string, string, []string, int64) bool, onFile func(string, int64)) (*warpReceiver, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("warpinator: listen %d: %w", port, err)
	}
	r := &warpReceiver{
		dest:    dest,
		info:    warpOurInfo(deviceName),
		onOffer: onOffer,
		onFile:  onFile,
		dial:    defaultWarpDial,
		grpc:    grpc.NewServer(),
		port:    ln.Addr().(*net.TCPAddr).Port,
	}
	warppb.RegisterWarpServer(r.grpc, r)
	go r.grpc.Serve(ln)
	return r, nil
}

func defaultWarpDial(peerAddr string) (warppb.WarpClient, func() error, error) {
	conn, err := grpc.NewClient(peerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return warppb.NewWarpClient(conn), conn.Close, nil
}

func (r *warpReceiver) close() {
	if r.grpc != nil {
		r.grpc.GracefulStop()
	}
}

func (r *warpReceiver) Ping(_ context.Context, _ *warppb.LookupName) (*warppb.VoidType, error) {
	return &warppb.VoidType{}, nil
}

func (r *warpReceiver) GetRemoteMachineInfo(_ context.Context, _ *warppb.LookupName) (*warppb.RemoteMachineInfo, error) {
	return &warppb.RemoteMachineInfo{DisplayName: r.info.DisplayName, UserName: r.info.UserName}, nil
}

func (r *warpReceiver) CheckDuplicateTransfer(_ context.Context, _ *warppb.LookupName) (*warppb.HaveDuplicate, error) {
	return &warppb.HaveDuplicate{Response: false}, nil
}

// ProcessTransferOpRequest is how a sender asks to send. We summarize it for
// the human accept prompt; on accept we dial the sender back and pull the
// files (Warpinator's receiver-pulls model).
func (r *warpReceiver) ProcessTransferOpRequest(ctx context.Context, req *warppb.TransferOpRequest) (*warppb.VoidType, error) {
	files, size := warpOfferSummary(warpTransferRequest{
		SenderName:   req.GetSenderName(),
		Size:         req.GetSize(),
		Count:        req.GetCount(),
		NameIfSingle: req.GetNameIfSingle(),
		TopDirs:      req.GetTopDirBasenames(),
	})
	addr := warpPeerAddr(ctx, r.port)
	alias := sanitizeDisplayName(req.GetSenderName())
	if alias == "" {
		alias = "Warpinator device"
	}
	if !r.onOffer(alias, addr, files, size) {
		return &warppb.VoidType{}, nil // declined; sender sees no pull
	}
	go r.pull(addr, req.GetInfo())
	return &warppb.VoidType{}, nil
}

// pull dials the sender and streams the files for the accepted op into dest.
func (r *warpReceiver) pull(peerAddr string, op *warppb.OpInfo) {
	client, closeConn, err := r.dial(peerAddr)
	if err != nil {
		logrus.WithError(err).Warn("warpinator: could not dial sender")
		return
	}
	defer closeConn()

	stream, err := client.StartTransfer(context.Background(), op)
	if err != nil {
		logrus.WithError(err).Warn("warpinator: StartTransfer failed")
		return
	}

	open := make(map[string]*os.File)
	names := make(map[string]string)
	defer func() {
		for _, f := range open {
			f.Close()
		}
	}()
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logrus.WithError(err).Warn("warpinator: chunk stream error")
			return
		}
		if err := r.writeChunk(chunk, open, names); err != nil {
			logrus.WithError(err).Warn("warpinator: could not write chunk")
			return
		}
	}
	for rel, f := range open {
		size, _ := f.Seek(0, io.SeekCurrent)
		f.Close()
		delete(open, rel)
		r.onFile(names[rel], size)
	}
}

// writeChunk appends one FileChunk to the right destination file, opening it
// (under a sanitized, deduped name) on first sight of its relative path.
func (r *warpReceiver) writeChunk(chunk *warppb.FileChunk, open map[string]*os.File, names map[string]string) error {
	rel := chunk.GetRelativePath()
	f, ok := open[rel]
	if !ok {
		name, err := safeUploadName(r.dest, rel)
		if err != nil {
			return err
		}
		f, err = os.OpenFile(filepath.Join(r.dest, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		open[rel] = f
		names[rel] = name
	}
	_, err := f.Write(chunk.GetChunk())
	return err
}

// warpPeerAddr derives the sender's gRPC address from the call context (its IP)
// plus the standard warp port.
func warpPeerAddr(ctx context.Context, _ int) string {
	host := "127.0.0.1"
	if p, ok := peer.FromContext(ctx); ok {
		if h, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			host = h
		}
	}
	return net.JoinHostPort(host, fmt.Sprint(warpDefaultPort))
}

// startWarpinator brings up the gated Warpinator adapter, reusing the shared
// consent + save pipeline. Returns a stop func or nil if it could not bind.
func (a *App) startWarpinator(dest string) func() {
	name := a.deviceName
	if name == "" {
		name = "krokodyl"
	}
	r, err := newWarpReceiver(dest, name, warpDefaultPort, a.localSendOffer, func(fname string, size int64) {
		a.tm.add(FileTransfer{
			ID:       "receive-warp-" + uuid.NewString(),
			Name:     fname,
			Files:    []string{fname},
			Size:     size,
			Status:   FileTransferStatusCompleted,
			Progress: 100,
		})
	})
	if err != nil {
		logrus.WithError(err).Info("Warpinator receiving unavailable")
		return nil
	}
	return r.close
}
