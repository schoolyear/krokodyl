<script lang="ts">
  import { onMount } from 'svelte'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'
  import { SendFile, ReceiveFile, GetTransfers, SelectFile, SelectDirectory } from '../wailsjs/go/main/App.js'

  interface FileTransfer {
    id: string
    filename: string
    size: number
    progress: number
    status: string
    code?: string
  }

  let transfers: FileTransfer[] = []
  let receiveCode: string = ''
  let destinationPath: string = ''
  let activeTab: 'send' | 'receive' = 'send'

  onMount(() => {
    loadTransfers()
    
    EventsOn('transfer:updated', (transfer: FileTransfer) => {
      const index = transfers.findIndex(t => t.id === transfer.id)
      if (index !== -1) {
        transfers[index] = transfer
        transfers = [...transfers]
      }
    })
  })

  async function loadTransfers() {
    transfers = await GetTransfers()
  }

  async function selectAndSendFile() {
    try {
      const filePath = await SelectFile()
      if (filePath) {
        await SendFile(filePath)
        await loadTransfers()
      }
    } catch (error) {
      console.error('Error sending file:', error)
    }
  }

  async function selectDestinationAndReceive() {
    try {
      const path = await SelectDirectory()
      if (path) {
        destinationPath = path
      }
    } catch (error) {
      console.error('Error selecting directory:', error)
    }
  }

  async function receiveFile() {
    if (!receiveCode.trim() || !destinationPath.trim()) {
      alert('Please enter a code and select a destination directory')
      return
    }
    
    try {
      await ReceiveFile(receiveCode, destinationPath)
      await loadTransfers()
      receiveCode = ''
    } catch (error) {
      console.error('Error receiving file:', error)
    }
  }

  function formatFileSize(bytes: number): string {
    if (bytes === 0) return '0 Bytes'
    const k = 1024
    const sizes = ['Bytes', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  }

  function getStatusColor(status: string): string {
    switch (status) {
      case 'completed': return '#4CAF50'
      case 'error': return '#f44336'
      case 'sending': case 'receiving': return '#2196F3'
      default: return '#FF9800'
    }
  }
</script>

<main>
  <div class="container">
    <h1>P2P File Transfer</h1>
    
    <div class="tabs">
      <button class="tab" class:active={activeTab === 'send'} on:click={() => activeTab = 'send'}>Send File</button>
      <button class="tab" class:active={activeTab === 'receive'} on:click={() => activeTab = 'receive'}>Receive File</button>
    </div>

    {#if activeTab === 'send'}
      <div class="tab-content">
        <h2>Send a File</h2>
        <p>Select a file to send to another peer</p>
        <button class="btn primary" on:click={selectAndSendFile}>Select & Send File</button>
      </div>
    {:else}
      <div class="tab-content">
        <h2>Receive a File</h2>
        <p>Enter the code from the sender and select where to save the file</p>
        
        <div class="input-group">
          <label for="code">Transfer Code:</label>
          <input type="text" id="code" bind:value={receiveCode} placeholder="Enter transfer code" />
        </div>

        <div class="input-group">
          <label for="destination">Destination:</label>
          <div class="destination-group">
            <input type="text" id="destination" bind:value={destinationPath} placeholder="Select destination directory" readonly />
            <button class="btn" on:click={selectDestinationAndReceive}>Browse</button>
          </div>
        </div>

        <button class="btn primary" on:click={receiveFile}>Receive File</button>
      </div>
    {/if}

    <div class="transfers">
      <h2>File Transfers</h2>
      {#if transfers.length === 0}
        <p class="empty">No transfers yet</p>
      {:else}
        {#each transfers as transfer}
          <div class="transfer-item">
            <div class="transfer-header">
              <span class="filename">{transfer.filename || 'Unknown'}</span>
              <span class="status" style="color: {getStatusColor(transfer.status)}">{transfer.status}</span>
            </div>
            
            {#if transfer.size > 0}
              <div class="file-info">
                <span class="size">{formatFileSize(transfer.size)}</span>
              </div>
            {/if}

            {#if transfer.code}
              <div class="code">
                <strong>Code:</strong> {transfer.code}
              </div>
            {/if}

            <div class="progress-bar">
              <div class="progress-fill" style="width: {transfer.progress}%"></div>
            </div>
            <span class="progress-text">{transfer.progress}%</span>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</main>

<style>
  .container {
    max-width: 800px;
    margin: 0 auto;
    padding: 20px;
  }

  h1 {
    text-align: center;
    color: #333;
    margin-bottom: 30px;
  }

  .tabs {
    display: flex;
    margin-bottom: 20px;
    border-bottom: 2px solid #ddd;
  }

  .tab {
    flex: 1;
    padding: 12px 20px;
    background: none;
    border: none;
    cursor: pointer;
    font-size: 16px;
    color: #666;
    transition: all 0.3s;
  }

  .tab:hover {
    background-color: #f5f5f5;
  }

  .tab.active {
    color: #2196F3;
    border-bottom: 2px solid #2196F3;
  }

  .tab-content {
    padding: 20px;
    background: #f9f9f9;
    border-radius: 8px;
    margin-bottom: 30px;
  }

  .tab-content h2 {
    margin-top: 0;
    color: #333;
  }

  .tab-content p {
    color: #666;
    margin-bottom: 20px;
  }

  .input-group {
    margin-bottom: 15px;
  }

  .input-group label {
    display: block;
    margin-bottom: 5px;
    font-weight: 600;
    color: #333;
  }

  .input-group input {
    width: 100%;
    padding: 10px;
    border: 1px solid #ddd;
    border-radius: 4px;
    font-size: 14px;
    box-sizing: border-box;
  }

  .input-group input:focus {
    outline: none;
    border-color: #2196F3;
  }

  .destination-group {
    display: flex;
    gap: 10px;
  }

  .destination-group input {
    flex: 1;
  }

  .btn {
    padding: 10px 20px;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 14px;
    transition: all 0.3s;
    background-color: #f0f0f0;
    color: #333;
  }

  .btn:hover {
    background-color: #e0e0e0;
  }

  .btn.primary {
    background-color: #2196F3;
    color: white;
  }

  .btn.primary:hover {
    background-color: #1976D2;
  }

  .transfers {
    margin-top: 30px;
  }

  .transfers h2 {
    color: #333;
    margin-bottom: 20px;
  }

  .empty {
    text-align: center;
    color: #666;
    font-style: italic;
  }

  .transfer-item {
    background: white;
    border: 1px solid #ddd;
    border-radius: 8px;
    padding: 15px;
    margin-bottom: 10px;
  }

  .transfer-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 10px;
  }

  .filename {
    font-weight: 600;
    color: #333;
  }

  .status {
    font-size: 12px;
    text-transform: uppercase;
    font-weight: 600;
  }

  .file-info {
    margin-bottom: 8px;
  }

  .size {
    font-size: 12px;
    color: #666;
  }

  .code {
    margin-bottom: 10px;
    padding: 8px;
    background-color: #f5f5f5;
    border-radius: 4px;
    font-family: monospace;
    font-size: 14px;
  }

  .progress-bar {
    width: 100%;
    height: 8px;
    background-color: #e0e0e0;
    border-radius: 4px;
    overflow: hidden;
    margin-bottom: 5px;
  }

  .progress-fill {
    height: 100%;
    background-color: #2196F3;
    transition: width 0.3s ease;
  }

  .progress-text {
    font-size: 12px;
    color: #666;
  }
</style>
