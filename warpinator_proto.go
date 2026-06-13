package main

import "strings"

// Warpinator (Linux Mint) interop — SCAFFOLD ONLY.
//
// Warpinator's transport is gRPC over protobuf (warpinator/warp.proto) with
// zeroconf/mDNS discovery and a cert-exchange auth step. The gRPC service stubs
// must be generated with `protoc` and validated against the real Warpinator app
// on a second device — neither is possible in this headless build. So this file
// carries only the hardware-free, testable pieces: discovery constants and the
// plain-Go shapes of the messages krokodyl would receive. The gRPC server that
// feeds these into the shared receive pipeline is the remaining work, captured
// in .claude/spikes/warpinator-runbook.md.

const (
	// Zeroconf service Warpinator advertises/browses.
	warpServiceType = "_warpinator._tcp"
	// Default gRPC transfer port; the cert/auth server runs at auth port.
	warpDefaultPort  = 42000
	warpDefaultAuth  = 42001
	warpAPIVersion   = "2"
	warpDefaultGroup = "Warpinator" // default group code (shared secret)
)

// warpRemoteMachineInfo mirrors RemoteMachineInfo — what we'd return to a peer
// asking who we are.
type warpRemoteMachineInfo struct {
	DisplayName string
	UserName    string
}

// warpTransferRequest mirrors the fields of TransferOpRequest a receiver needs
// to render an accept prompt (sender, file count/size, single-file name).
type warpTransferRequest struct {
	SenderName   string
	Size         uint64
	Count        uint64
	NameIfSingle string
	TopDirs      []string
}

// warpServiceTXT builds the mDNS TXT records Warpinator expects on the service:
// the API version and the hostname; the receiver uses these to dial back.
func warpServiceTXT(hostname string) map[string]string {
	return map[string]string{
		"api-version": warpAPIVersion,
		"hostname":    hostname,
		"type":        "real", // "flush" is used only for shutdown announcements
	}
}

// warpOfferSummary turns a transfer request into the (files, totalSize) the
// shared nearby:offer consent prompt expects.
func warpOfferSummary(req warpTransferRequest) (files []string, size int64) {
	if req.Count == 1 && req.NameIfSingle != "" {
		files = []string{sanitizeDisplayName(req.NameIfSingle)}
	} else {
		for _, d := range req.TopDirs {
			files = append(files, sanitizeDisplayName(d))
		}
	}
	if len(files) == 0 {
		files = []string{"(files)"}
	}
	if req.Size > 0 && req.Size < 1<<62 {
		size = int64(req.Size)
	}
	return files, size
}

// warpOurInfo is the RemoteMachineInfo krokodyl would present.
func warpOurInfo(deviceName string) warpRemoteMachineInfo {
	name := strings.TrimSpace(deviceName)
	if name == "" {
		name = "krokodyl"
	}
	return warpRemoteMachineInfo{DisplayName: name, UserName: "krokodyl"}
}
