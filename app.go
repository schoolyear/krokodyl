package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/schollz/croc/v10/src/croc"
	"github.com/schollz/croc/v10/src/utils"
	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type FileTransfer struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Files    []string           `json:"files"`
	Size     int64              `json:"size"`
	Progress int                `json:"progress"`
	Status   FileTransferStatus `json:"status"`
	Code     string             `json:"code,omitempty"`
}

// App struct
type (
	App struct {
		ctx       context.Context
		transfers []FileTransfer

		sync.RWMutex
	}

	FileTransferStatus string
)

const (
	FileTransferStatusPreparing FileTransferStatus = "preparing"
	FileTransferStatusWaiting   FileTransferStatus = "waiting"
	FileTransferStatusSending   FileTransferStatus = "sending"
	FileTransferStatusReceiving FileTransferStatus = "receiving"

	FileTransferStatusError     FileTransferStatus = "error"
	FileTransferStatusCompleted FileTransferStatus = "completed"
)

const (
	TransferEventUpdated string = "transfer:updated"
)

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.transfers = make([]FileTransfer, 0)
}

func (a *App) SendFile(filePath string) (string, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to stat file: %s", filePath)
	}

	transfer := FileTransfer{
		ID:       a.getSendId(),
		Name:     fileInfo.Name(),
		Files:    []string{fileInfo.Name()},
		Size:     fileInfo.Size(),
		Progress: 0,
		Status:   FileTransferStatusPreparing,
	}

	a.Lock()
	a.transfers = append([]FileTransfer{transfer}, a.transfers...)
	a.Unlock()

	go a.performSend(&a.transfers[0], filePath)

	return transfer.ID, nil
}

func (a *App) performSend(transfer *FileTransfer, filePath string) {
	transfer.Code = utils.GetRandomName()
	transfer.Status = FileTransferStatusWaiting
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)

	options := croc.Options{
		IsSender:       true,
		SharedSecret:   transfer.Code,
		Debug:          false,
		NoPrompt:       true,
		RelayAddress:   "croc.schollz.com:9009",
		RelayPorts:     []string{"9009", "9010", "9011", "9012", "9013"},
		RelayPassword:  "pass123",
		NoMultiplexing: false,
		DisableLocal:   false,
		OnlyLocal:      false,
		IgnoreStdin:    true,
		Overwrite:      true,
		Curve:          "p256",
		HashAlgorithm:  "xxhash",
	}

	crocClient, err := croc.New(options)
	if err != nil {
		logrus.WithError(err).Error("error while creating croc client")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	filesInfo, emptyFolders, totalFolders, err := croc.GetFilesInfo(
		[]string{filePath},
		false,
		false,
		[]string{},
	)
	if err != nil {
		logrus.WithError(err).Error("error while getting files info")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	var fileNames []string
	for _, f := range filesInfo {
		fileNames = append(fileNames, f.Name)
	}
	transfer.Files = fileNames
	transfer.Progress = 0 // Set progress to 0% for the "waiting" state
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)

	err = crocClient.Send(filesInfo, emptyFolders, totalFolders)
	if err != nil {
		logrus.WithError(err).Error("error sending files")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	transfer.Status = FileTransferStatusCompleted
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
}

func (a *App) getSendId() string {
	return fmt.Sprintf("send-%d", a.Len())
}

func (a *App) getReceiveId() string {
	return fmt.Sprintf("receive-%d", a.Len())
}

func (a *App) GetTransfers() []FileTransfer {
	a.RLock()
	defer a.RUnlock()

	return a.transfers
}

// Len returns the number of transfers in history
func (a *App) Len() int {
	a.RLock()
	defer a.RUnlock()

	return len(a.transfers)
}

func (a *App) ReceiveFile(code, destinationPath string) (string, error) {
	transfer := FileTransfer{
		ID:       a.getReceiveId(),
		Code:     code,
		Progress: 0,
		Status:   FileTransferStatusPreparing,
		Name:     "Preparing to receive...",
		Files:    []string{},
	}

	a.Lock()
	a.transfers = append([]FileTransfer{transfer}, a.transfers...)
	a.Unlock()

	go a.performReceive(&a.transfers[0], code, destinationPath)

	return transfer.ID, nil
}

func (a *App) performReceive(transfer *FileTransfer, code, destinationPath string) {
	transfer.Status = FileTransferStatusReceiving
	transfer.Name = "Receiving..."
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)

	currentDir, err := os.Getwd()
	if err != nil {
		logrus.WithError(err).Error("error getting current directory")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	err = os.Chdir(destinationPath)
	if err != nil {
		logrus.WithError(err).Errorf("error changing directory to %s", destinationPath)
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	defer func() {
		os.Chdir(currentDir)
	}()

	options := croc.Options{
		IsSender:       false,
		SharedSecret:   code,
		Debug:          false,
		NoPrompt:       true,
		RelayAddress:   "croc.schollz.com:9009",
		RelayPorts:     []string{"9009", "9010", "9011", "9012", "9013"},
		RelayPassword:  "pass123",
		NoMultiplexing: false,
		DisableLocal:   false,
		OnlyLocal:      false,
		IgnoreStdin:    true,
		Overwrite:      true,
		Curve:          "p256",
		HashAlgorithm:  "xxhash",
	}

	crocClient, err := croc.New(options)
	if err != nil {
		logrus.WithError(err).Error("error creating croc client")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	transfer.Progress = 50
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)

	errCh := make(chan error, 1)
	go func() {
		errCh <- crocClient.Receive()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			logrus.WithError(err).Error("error receiving files")
			transfer.Status = FileTransferStatusError
			runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
			return
		}
	case <-time.After(30 * time.Second):
		logrus.Error("timeout while waiting for sender")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	// We don't know the filenames when receiving with croc v10 this way
	transfer.Name = "Completed"
	transfer.Files = []string{"File received successfully"}
	transfer.Status = FileTransferStatusCompleted
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
}

func (a *App) SelectFile() (string, error) {
	selection, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select file to send",
	})

	if err != nil {
		return "", errors.Wrap(err, "failed to open file dialog")
	}

	return selection, nil
}

func (a *App) SelectDirectory() (string, error) {
	selection, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select destination directory",
	})

	if err != nil {
		return "", errors.Wrap(err, "failed to open directory dialog")
	}

	return selection, nil
}

func (a *App) GetDefaultDownloadPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	return filepath.Join(homeDir, "Downloads"), nil
}
