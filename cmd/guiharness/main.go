// guiharness runs a real send+receive transfer through krokodyl's worker
// mode from a GUI-subsystem process (build with -ldflags "-H windowsgui").
//
// Launched without a console its std handles are invalid — the exact
// environment of the double-clicked app, and the environment in which the
// "write /dev/stderr: The handle is invalid" transfer failure occurred.
// Terminal-launched tests cannot reproduce that; this harness can. It spawns
// the workers exactly like App.runWorkerJob does (stdin job JSON, stdout
// event stream, Stderr left nil) and writes a JSON verdict to a result file.
//
// Usage: guiharness <krokodyl.exe> <file-to-send> <work-dir> <result-file>
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type workerJob struct {
	Mode       string   `json:"mode"`
	Code       string   `json:"code"`
	Paths      []string `json:"paths,omitempty"`
	StagingDir string   `json:"stagingDir,omitempty"`
}

type workerEvent struct {
	Type     string `json:"type"`
	Progress int    `json:"progress,omitempty"`
	Message  string `json:"message,omitempty"`
}

type result struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error,omitempty"`
	ReceivedBytes    int64  `json:"receivedBytes"`
	SenderProgress   int    `json:"senderMaxProgress"`
	ReceiverProgress int    `json:"receiverMaxProgress"`
}

func main() {
	if len(os.Args) != 5 {
		os.Exit(2)
	}
	exe, sendFile, workDir, resultFile := os.Args[1], os.Args[2], os.Args[3], os.Args[4]

	res := run(exe, sendFile, workDir)
	data, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(resultFile, data, 0o644)
	if !res.OK {
		os.Exit(1)
	}
}

func run(exe, sendFile, workDir string) result {
	code := fmt.Sprintf("%d-harness-test-code", time.Now().UnixNano()%10000)

	stagingDir := filepath.Join(workDir, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return result{Error: "mkdir staging: " + err.Error()}
	}

	sendErrCh := make(chan error, 1)
	sendProgCh := make(chan int, 1)
	go func() {
		prog, err := runWorker(exe, workerJob{Mode: "send", Code: code, Paths: []string{sendFile}})
		sendProgCh <- prog
		sendErrCh <- err
	}()

	// Give the sender a moment to register with the relay/local discovery.
	time.Sleep(2 * time.Second)

	recvProg, recvErr := runWorker(exe, workerJob{Mode: "receive", Code: code, StagingDir: stagingDir})
	sendErr := <-sendErrCh
	sendProg := <-sendProgCh

	res := result{SenderProgress: sendProg, ReceiverProgress: recvProg}
	if sendErr != nil {
		res.Error = "send: " + sendErr.Error()
		return res
	}
	if recvErr != nil {
		res.Error = "receive: " + recvErr.Error()
		return res
	}

	want, err := os.Stat(sendFile)
	if err != nil {
		res.Error = "stat source: " + err.Error()
		return res
	}
	got, err := os.Stat(filepath.Join(stagingDir, filepath.Base(sendFile)))
	if err != nil {
		res.Error = "received file missing: " + err.Error()
		return res
	}
	res.ReceivedBytes = got.Size()
	if got.Size() != want.Size() {
		res.Error = fmt.Sprintf("size mismatch: sent %d, received %d", want.Size(), got.Size())
		return res
	}

	res.OK = true
	return res
}

// runWorker mirrors App.runWorkerJob: same spawn shape, Stderr deliberately
// left nil (the fix under test). Returns the max progress seen and the
// worker's error, with any worker-reported error message attached.
func runWorker(exe string, job workerJob) (int, error) {
	cmd := exec.Command(exe, "--transfer-worker")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	if err := json.NewEncoder(stdin).Encode(job); err != nil {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
		return 0, err
	}
	stdin.Close()

	maxProgress := 0
	errMsg := ""
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev workerEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.Type == "error" {
			errMsg = ev.Message
		}
		if ev.Progress > maxProgress {
			maxProgress = ev.Progress
		}
	}

	if err := cmd.Wait(); err != nil {
		if errMsg != "" {
			return maxProgress, fmt.Errorf("%s (%w)", errMsg, err)
		}
		return maxProgress, err
	}
	return maxProgress, nil
}
