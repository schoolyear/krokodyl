package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/schollz/croc/v10/src/croc"
	"github.com/schollz/croc/v10/src/utils"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type FileTransfer struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Progress int    `json:"progress"`
	Status   string `json:"status"`
	Code     string `json:"code,omitempty"`
}

// App struct
type App struct {
	ctx       context.Context
	transfers map[string]*FileTransfer
	mu        sync.RWMutex
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		transfers: make(map[string]*FileTransfer),
	}
}

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
		ID:       fmt.Sprintf("send-%d", len(a.transfers)),
		Filename: fileInfo.Name(),
		Size:     fileInfo.Size(),
		Progress: 0,
		Status:   "preparing",
	}

	a.mu.Lock()
	a.transfers[transfer.ID] = transfer
	a.mu.Unlock()

	go a.performSend(transfer, filePath)

	return transfer.ID, nil
}

func (a *App) performSend(transfer *FileTransfer, filePath string) {
	transfer.Status = "sending"
	runtime.EventsEmit(a.ctx, "transfer:updated", transfer)

	generatedCode := utils.GetRandomName()
	
	options := croc.Options{
		IsSender:         true,
		SharedSecret:     generatedCode,
		Debug:            false,
		NoPrompt:         true,
		RelayAddress:     "croc.schollz.com:9009",
		RelayPorts:       []string{"9009", "9010", "9011", "9012", "9013"},
		RelayPassword:    "pass123",
		NoMultiplexing:   false,
		DisableLocal:     false,
		OnlyLocal:        false,
		IgnoreStdin:      true,
		Overwrite:        true,
		Curve:            "p256",
		HashAlgorithm:    "xxhash",
	}

	crocClient, err := croc.New(options)
	if err != nil {
		fmt.Printf("Error creating croc client: %v\n", err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	filesInfo, emptyFolders, totalFolders, err := croc.GetFilesInfo(
		[]string{filePath},
		false,
		false,
		[]string{},
	)
	if err != nil {
		fmt.Printf("Error getting files info: %v\n", err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	transfer.Progress = 25
	runtime.EventsEmit(a.ctx, "transfer:updated", transfer)

	err = crocClient.Send(filesInfo, emptyFolders, totalFolders)
	if err != nil {
		fmt.Printf("Error sending files: %v\n", err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	transfer.Code = crocClient.Options.SharedSecret
	transfer.Status = "completed"
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
}

func (a *App) ReceiveFile(code, destinationPath string) (string, error) {
	transfer := &FileTransfer{
		ID:       fmt.Sprintf("receive-%d", len(a.transfers)),
		Code:     code,
		Progress: 0,
		Status:   "preparing",
	}

	a.mu.Lock()
	a.transfers[transfer.ID] = transfer
	a.mu.Unlock()

	go a.performReceive(transfer, code, destinationPath)

	return transfer.ID, nil
}

func (a *App) performReceive(transfer *FileTransfer, code, destinationPath string) {
	transfer.Status = "receiving"
	transfer.Filename = "Receiving file..."
	runtime.EventsEmit(a.ctx, "transfer:updated", transfer)

	currentDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current directory: %v\n", err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	err = os.Chdir(destinationPath)
	if err != nil {
		fmt.Printf("Error changing directory to %s: %v\n", destinationPath, err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	defer func() {
		os.Chdir(currentDir)
	}()

	options := croc.Options{
		IsSender:         false,
		SharedSecret:     code,
		Debug:            false,
		NoPrompt:         true,
		RelayAddress:     "croc.schollz.com:9009",
		RelayPorts:       []string{"9009", "9010", "9011", "9012", "9013"},
		RelayPassword:    "pass123",
		NoMultiplexing:   false,
		DisableLocal:     false,
		OnlyLocal:        false,
		IgnoreStdin:      true,
		Overwrite:        true,
		Curve:            "p256",
		HashAlgorithm:    "xxhash",
	}

	crocClient, err := croc.New(options)
	if err != nil {
		fmt.Printf("Error creating croc client: %v\n", err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	transfer.Progress = 50
	runtime.EventsEmit(a.ctx, "transfer:updated", transfer)

	err = crocClient.Receive()
	if err != nil {
		fmt.Printf("Error receiving files: %v\n", err)
		transfer.Status = "error"
		runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
		return
	}

	transfer.Filename = "File received successfully"
	transfer.Status = "completed"
	transfer.Progress = 100
	runtime.EventsEmit(a.ctx, "transfer:updated", transfer)
}

func (a *App) GetTransfers() []*FileTransfer {
	a.mu.RLock()
	defer a.mu.RUnlock()

	transfers := make([]*FileTransfer, 0, len(a.transfers))
	for _, transfer := range a.transfers {
		transfers = append(transfers, transfer)
	}
	return transfers
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
		return "", err
	}

	return selection, nil
}

func (a *App) SelectDirectory() (string, error) {
	selection, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select destination directory",
	})

	if err != nil {
		return "", err
	}

	return selection, nil
}
