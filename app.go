package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/schollz/croc/v10/src/croc"
	"github.com/schollz/croc/v10/src/utils"
	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type FileTransfer struct {
	ID       string             `json:"id"`
	Filename string             `json:"filename"`
	Size     int64              `json:"size"`
	Progress int                `json:"progress"`
	Status   FileTransferStatus `json:"status"`
	Code     string             `json:"code,omitempty"`
}

// App struct
type (
	App struct {
		ctx              context.Context
		numFilesSent     int64
		numFilesReceived int64
	}

	FileTransferStatus string
)

const (
	FileTransferStatusPreparing FileTransferStatus = "preparing"
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
}

func (a *App) SendFile(filePath string) (string, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}

	transfer := &FileTransfer{
		ID:       a.getSendId(),
		Filename: fileInfo.Name(),
		Size:     fileInfo.Size(),
		Progress: 0,
		Status:   FileTransferStatusPreparing,
	}

	go a.performSend(transfer, filePath)

	return transfer.ID, nil
}

func (a *App) performSend(transfer *FileTransfer, filePath string) {
	transfer.Status = FileTransferStatusSending

	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)

	generatedCode := utils.GetRandomName()
	options := croc.Options{
		IsSender:       true,
		SharedSecret:   generatedCode,
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
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		transfer.Status = FileTransferStatusError
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
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		transfer.Status = FileTransferStatusError
		return
	}

	transfer.Progress = 25
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)

	err = crocClient.Send(filesInfo, emptyFolders, totalFolders)
	if err != nil {
		logrus.WithError(err).Error("error sending files")
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		transfer.Status = FileTransferStatusError
		return
	}

	transfer.Code = crocClient.Options.SharedSecret
	transfer.Status = FileTransferStatusCompleted
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
}

func (a *App) getSendId() string {
	return fmt.Sprintf("send-%d", atomic.AddInt64(&a.numFilesSent, 1))
}

func (a *App) getReceiveId() string {
	return fmt.Sprintf("receive-%d", atomic.AddInt64(&a.numFilesReceived, 1))
}

func (a *App) ReceiveFile(code, destinationPath string) (string, error) {
	transfer := &FileTransfer{
		ID:       a.getReceiveId(),
		Code:     code,
		Progress: 0,
		Status:   FileTransferStatusPreparing,
	}

	go a.performReceive(transfer, code, destinationPath)

	return transfer.ID, nil
}

func (a *App) performReceive(transfer *FileTransfer, code, destinationPath string) {
	transfer.Status = FileTransferStatusReceiving
	transfer.Filename = "Receiving file..."
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

	err = crocClient.Receive()
	if err != nil {
		logrus.WithError(err).Error("error receiving files")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	transfer.Filename = "File received successfully"
	transfer.Status = FileTransferStatusCompleted
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
}

func (a *App) SelectFile() (string, error) {
	selection, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select file to send",
		Filters: []runtime.FileFilter{
			{
				DisplayName: "All Files",
				Pattern:     "*",
			},
		},
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
