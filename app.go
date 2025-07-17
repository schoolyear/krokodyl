package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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

type OverwritePrompt struct {
	TransferID string `json:"transferId"`
	FileName   string `json:"fileName"`
	OldSize    int64  `json:"oldSize"`
	NewSize    int64  `json:"newSize"`
	Diff       string `json:"diff"`
}

// App struct
type (
	App struct {
		ctx                context.Context
		transfers          []FileTransfer
		overwriteResponses map[string]chan string

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
	TransferEventUpdated   string = "transfer:updated"
	TransferEventOverwrite string = "transfer:overwrite"
)

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.transfers = make([]FileTransfer, 0)
	a.overwriteResponses = make(map[string]chan string)
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

	tempDir, err := os.MkdirTemp("", "krokodyl-")
	if err != nil {
		logrus.WithError(err).Error("error creating temp directory")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}
	defer os.RemoveAll(tempDir)

	// Change to the temporary directory to receive the file
	if err := os.Chdir(tempDir); err != nil {
		logrus.WithError(err).Errorf("error changing to temp directory %s", tempDir)
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	crocClient, err := croc.New(options)
	if err != nil {
		logrus.WithError(err).Error("error creating croc client")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	if err := crocClient.Receive(); err != nil {
		logrus.WithError(err).Error("error receiving files")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	// Now that the file is in the temp directory, get its info
	receivedFileInfos, err := listFiles(tempDir)
	if err != nil {
		logrus.WithError(err).Error("error listing files in temp directory")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	var receivedFiles []os.FileInfo
	for _, info := range receivedFileInfos {
		receivedFiles = append(receivedFiles, info)
	}

	if len(receivedFiles) == 0 {
		logrus.Error("no files received")
		transfer.Status = FileTransferStatusError
		runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
		return
	}

	var fileNames []string
	var totalSize int64
	for _, fileInfo := range receivedFiles {
		sourcePath := filepath.Join(tempDir, fileInfo.Name())
		destPath := filepath.Join(destinationPath, fileInfo.Name())

		// Check if the file already exists
		if existingInfo, err := os.Stat(destPath); err == nil {
			// File exists, prompt for overwrite
			responseChan := make(chan string)
			a.Lock()
			a.overwriteResponses[transfer.ID] = responseChan
			a.Unlock()

			diff, err := getFileDiff(destPath, sourcePath)
			if err != nil {
				logrus.WithError(err).Warnf("could not get file diff")
				diff = "Could not generate file difference."
			}

			runtime.EventsEmit(a.ctx, TransferEventOverwrite, OverwritePrompt{
				TransferID: transfer.ID,
				FileName:   fileInfo.Name(),
				OldSize:    existingInfo.Size(),
				NewSize:    fileInfo.Size(),
				Diff:       diff,
			})

			// Wait for the user's response
			response := <-responseChan
			if response != "yes" {
				// User chose not to overwrite, so we skip this file
				logrus.Infof("User chose not to overwrite %s", fileInfo.Name())
				continue // Move to the next file
			}
		}

		// Ensure the destination directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), os.ModePerm); err != nil {
			logrus.WithError(err).Errorf("failed to create destination directory for %s", destPath)
			transfer.Status = FileTransferStatusError
			runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
			return
		}

		if err := os.Rename(sourcePath, destPath); err != nil {
			logrus.WithError(err).Errorf("failed to move file from %s to %s", sourcePath, destPath)
			transfer.Status = FileTransferStatusError
			runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
			return
		}

		fileNames = append(fileNames, fileInfo.Name())
		totalSize += fileInfo.Size()
	}

	if len(fileNames) > 0 {
		transfer.Name = fileNames[0]
		if len(fileNames) > 1 {
			transfer.Name = fmt.Sprintf("%s and %d more", fileNames[0], len(fileNames)-1)
		}
	} else {
		transfer.Name = "File received"
	}

	transfer.Files = fileNames
	transfer.Size = totalSize
	transfer.Status = FileTransferStatusCompleted
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, TransferEventUpdated, transfer)
}

func listFiles(dir string) (map[string]os.FileInfo, error) {
	files := make(map[string]os.FileInfo)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files[path] = info
		}
		return nil
	})
	return files, err
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

func (a *App) RespondToOverwrite(transferID string, response string) {
	a.RLock()
	responseChan, ok := a.overwriteResponses[transferID]
	a.RUnlock()

	if ok {
		responseChan <- response
		a.Lock()
		delete(a.overwriteResponses, transferID)
		a.Unlock()
	}
}

func (a *App) GetDefaultDownloadPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	return filepath.Join(homeDir, "Downloads"), nil
}

func getFileDiff(file1, file2 string) (string, error) {
	f1, err := os.ReadFile(file1)
	if err != nil {
		return "", err
	}
	f2, err := os.ReadFile(file2)
	if err != nil {
		return "", err
	}

	// For simplicity, we'll just return a basic line-by-line comparison
	// In a real app, you might use a proper diffing library
	diff := ""
	lines1 := string(f1)
	lines2 := string(f2)

	if lines1 == lines2 {
		return "Files are identical.", nil
	}

	diff += "--- a/" + filepath.Base(file1) + "\n"
	diff += "+++ b/" + filepath.Base(file2) + "\n"
	diff += "File content differs."

	return diff, nil
}
