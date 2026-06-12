package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/schollz/croc/v10/src/croc"
	"github.com/schollz/croc/v10/src/models"
	"github.com/sirupsen/logrus"
)

// Each transfer runs in a child process of this same binary ("worker mode").
// The croc library offers no cancellation API, reports progress only through
// exported Client fields, and writes received files to the process-global
// working directory — a subprocess gives us kill-based cancel, per-transfer
// cwd isolation and crash isolation in one move. The parent sends one
// workerJob as JSON on stdin; the worker streams workerEvent JSON lines on
// stdout (stdout is reserved for the protocol, logs go to stderr + log file).

const workerFlag = "--transfer-worker"

type workerJob struct {
	Mode       string   `json:"mode"` // "send" or "receive"
	Code       string   `json:"code"`
	Paths      []string `json:"paths,omitempty"`      // send: files/folders to transfer
	StagingDir string   `json:"stagingDir,omitempty"` // receive: directory croc writes into
}

type workerEvent struct {
	Type     string   `json:"type"` // "files", "progress", "done", "error"
	Files    []string `json:"files,omitempty"`
	Size     int64    `json:"size,omitempty"`
	Sent     int64    `json:"sent,omitempty"`
	Progress int      `json:"progress,omitempty"`
	Speed    int64    `json:"speed,omitempty"` // bytes per second
	Message  string   `json:"message,omitempty"`
}

const progressPollInterval = 200 * time.Millisecond

// isWorkerProcess reports whether this process was spawned as a transfer
// worker. Checked before the Wails app starts.
func isWorkerProcess() bool {
	return hasWorkerFlag(os.Args[1:])
}

func hasWorkerFlag(args []string) bool {
	for _, arg := range args {
		if arg == workerFlag {
			return true
		}
	}
	return false
}

// runWorker executes one transfer job and returns the process exit code.
func runWorker() int {
	emitter := &eventEmitter{enc: json.NewEncoder(os.Stdout)}

	var job workerJob
	if err := json.NewDecoder(os.Stdin).Decode(&job); err != nil {
		emitter.error(fmt.Sprintf("invalid worker job: %s", err))
		return 1
	}

	var err error
	switch job.Mode {
	case "send":
		err = workerSend(emitter, job)
	case "receive":
		err = workerReceive(emitter, job)
	default:
		err = fmt.Errorf("unknown worker mode %q", job.Mode)
	}

	if err != nil {
		logrus.WithError(err).Errorf("worker %s failed", job.Mode)
		emitter.error(err.Error())
		return 1
	}

	emitter.emit(workerEvent{Type: "done"})
	return 0
}

func workerSend(emitter *eventEmitter, job workerJob) error {
	client, err := croc.New(newCrocOptions(true, job.Code))
	if err != nil {
		return fmt.Errorf("failed to create croc client: %w", err)
	}

	filesInfo, emptyFolders, totalFolders, err := croc.GetFilesInfo(job.Paths, false, false, []string{})
	if err != nil {
		return fmt.Errorf("failed to read files to send: %w", err)
	}

	var names []string
	var total int64
	for _, f := range filesInfo {
		names = append(names, f.Name)
		total += f.Size
	}
	emitter.emit(workerEvent{Type: "files", Files: names, Size: total})

	stop := startProgressPoller(emitter, client, total)
	defer stop()

	if err := client.Send(filesInfo, emptyFolders, totalFolders); err != nil {
		return fmt.Errorf("sending failed: %w", err)
	}
	return nil
}

func workerReceive(emitter *eventEmitter, job workerJob) error {
	// Safe here: cwd is per-process and this process runs exactly one
	// transfer. croc can only receive into the working directory.
	if err := os.Chdir(job.StagingDir); err != nil {
		return fmt.Errorf("failed to enter staging directory: %w", err)
	}

	client, err := croc.New(newCrocOptions(false, job.Code))
	if err != nil {
		return fmt.Errorf("failed to create croc client: %w", err)
	}

	stop := startProgressPoller(emitter, client, 0)
	defer stop()

	if err := client.Receive(); err != nil {
		return fmt.Errorf("receiving failed: %w", err)
	}
	return nil
}

// newCrocOptions mirrors the croc CLI defaults, including the IPv6 relay the
// previous hardcoded configuration was missing.
func newCrocOptions(isSender bool, secret string) croc.Options {
	return croc.Options{
		IsSender:      isSender,
		SharedSecret:  secret,
		Debug:         false,
		NoPrompt:      true,
		RelayAddress:  models.DEFAULT_RELAY,
		RelayAddress6: models.DEFAULT_RELAY6,
		RelayPorts:    []string{"9009", "9010", "9011", "9012", "9013"},
		RelayPassword: models.DEFAULT_PASSPHRASE,
		IgnoreStdin:   true,
		Overwrite:     true,
		Curve:         "p256",
		HashAlgorithm: "xxhash",
	}
}

// startProgressPoller samples the croc client's exported counters and emits
// progress events until stopped. knownTotal of 0 means the total becomes
// known only after the handshake (receive side) and is read from the client.
func startProgressPoller(emitter *eventEmitter, client *croc.Client, knownTotal int64) (stop func()) {
	done := make(chan struct{})
	var once sync.Once

	go func() {
		ticker := time.NewTicker(progressPollInterval)
		defer ticker.Stop()

		var lastSent int64
		var lastTime = time.Now()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// croc does not synchronize these exported counters; a torn
				// read here only skews one progress sample and the worker is
				// an isolated process, so this is acceptable.
				total := knownTotal
				if total == 0 {
					for _, f := range client.FilesToTransfer {
						total += f.Size
					}
				}
				sent := client.TotalSent
				if sent <= 0 && total == 0 {
					continue
				}

				now := time.Now()
				var speed int64
				if dt := now.Sub(lastTime).Seconds(); dt > 0 && sent >= lastSent {
					speed = int64(float64(sent-lastSent) / dt)
				}
				lastSent, lastTime = sent, now

				emitter.emit(workerEvent{
					Type:     "progress",
					Sent:     sent,
					Size:     total,
					Progress: progressPercent(sent, total),
					Speed:    speed,
				})
			}
		}
	}()

	return func() { once.Do(func() { close(done) }) }
}

// progressPercent caps at 99 — only the "done" event marks 100%.
func progressPercent(sent, total int64) int {
	if total <= 0 || sent <= 0 {
		return 0
	}
	pct := int(sent * 100 / total)
	if pct > 99 {
		pct = 99
	}
	return pct
}

type eventEmitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (e *eventEmitter) emit(ev workerEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.enc.Encode(ev); err != nil {
		logrus.WithError(err).Error("worker failed to emit event")
	}
}

func (e *eventEmitter) error(msg string) {
	e.emit(workerEvent{Type: "error", Message: msg})
}
