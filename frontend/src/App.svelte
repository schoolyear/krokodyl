<script lang="ts">
  import { onMount } from 'svelte'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'
  import { SendFile, ReceiveFile, GetTransfers, SelectFile, SelectDirectory, GetDefaultDownloadPath } from '../wailsjs/go/main/App.js'

  interface FileTransfer {
    id: string
    name: string
    files: string[]
    size: number
    progress: number
    status: string
    code?: string
  }

  let transfers: FileTransfer[] = []
  let receiveCode: string = ''
  let destinationPath: string = ''
  let activeTab: 'send' | 'receive' = 'send'
  let isSending = false;
  let isReceiving = false;
  let toastMessage = '';
  let toastType: 'success' | 'error' | 'info' = 'info';

  onMount(async () => {
    loadTransfers()
    try {
      destinationPath = await GetDefaultDownloadPath();
    } catch (error) {
      console.error("Could not get default download path", error)
    }
    
    EventsOn('transfer:updated', (transfer: FileTransfer) => {
      const index = transfers.findIndex(t => t.id === transfer.id)
      if (index !== -1) {
        transfers[index] = transfer
        transfers = [...transfers]
      } else {
        transfers = [transfer, ...transfers];
      }

      if (transfer.status === 'completed') {
        showToast('Transfer completed! 🎉', 'success');
        if (transfer.id.startsWith('send')) isSending = false;
        if (transfer.id.startsWith('receive')) isReceiving = false;
      } else if (transfer.status === 'error') {
        showToast('Transfer failed. Please try again. 😢', 'error');
        if (transfer.id.startsWith('send')) isSending = false;
        if (transfer.id.startsWith('receive')) isReceiving = false;
      }
    })
  })

  async function loadTransfers() {
    transfers = await GetTransfers()
  }

  async function selectAndSendFile() {
    if (isSending) return;
    try {
      const filePath = await SelectFile()
      if (filePath) {
        showToast('File selected! Generating code...', 'info');
        isSending = true;
        await SendFile(filePath)
      }
    } catch (error) {
      console.error('Error sending file:', error)
      showToast('Failed to select file.', 'error');
      isSending = false;
    }
  }

  async function selectDestinationAndReceive() {
    try {
      const path = await SelectDirectory()
      if (path) {
        destinationPath = path
        showToast('Destination selected!', 'info');
      }
    } catch (error) {
      console.error('Error selecting directory:', error)
      showToast('Failed to select destination.', 'error');
    }
  }

  async function receiveFile() {
    if (isReceiving || !receiveCode.trim() || !destinationPath.trim()) {
      showToast('Please enter a code and select a destination.', 'error');
      return
    }
    
    try {
      showToast('Starting download...', 'info');
      isReceiving = true;
      await ReceiveFile(receiveCode, destinationPath)
      receiveCode = ''
    } catch (error) {
      console.error('Error receiving file:', error)
      showToast('Failed to receive file.', 'error');
      isReceiving = false;
    }
  }

  function formatFileSize(bytes: number): string {
    if (bytes === 0) return '0 Bytes'
    const k = 1024
    const sizes = ['Bytes', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  }

  function getStatusInfo(status: string): { color: string; icon: string } {
    switch (status) {
      case 'completed': return { color: 'var(--color-green)', icon: '✅' }
      case 'error': return { color: 'var(--color-red)', icon: '❌' }
      case 'waiting':
        return { color: 'var(--color-yellow)', icon: '⌛' }
      case 'sending':
      case 'receiving':
        return { color: 'var(--color-primary)', icon: '⏳' }
      case 'preparing':
        return { color: 'var(--color-yellow)', icon: '⌛' }
      default: return { color: 'var(--color-text-dim)', icon: '❓' }
    }
  }

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text);
    showToast('Copied to clipboard! 👍', 'success');
  }

  function showToast(message: string, type: 'success' | 'error' | 'info' = 'info') {
    if (toastMessage) return; // Prevent multiple toasts at once
    toastMessage = message;
    toastType = type;
    setTimeout(() => {
      toastMessage = '';
    }, 3000);
  }
</script>

<main>
  <div class="header">
    <h1>🐊 Krokodyl</h1>
    <p>Secure, fast, and simple P2P file sharing.</p>
  </div>

  <div class="card">
    <div class="tabs">
      <button class="tab" class:active={activeTab === 'send'} on:click={() => activeTab = 'send'}>
        <span>📤</span> Send
      </button>
      <button class="tab" class:active={activeTab === 'receive'} on:click={() => activeTab = 'receive'}>
        <span>📥</span> Receive
      </button>
    </div>

    <div class="tab-content">
      {#if activeTab === 'send'}
        <div class="action-section">
          <h2>Send a File</h2>
          <p>Select a file to generate a secure transfer code.</p>
          <button class="btn primary" on:click={selectAndSendFile} disabled={isSending}>
            {#if isSending}
              <div class="spinner"></div>
              <span>Sending...</span>
            {:else}
              <span>📁 Select & Send File</span>
            {/if}
          </button>
        </div>
      {:else}
        <div class="action-section">
          <h2>Receive a File</h2>
          <p>Enter a transfer code and choose where to save the file.</p>
          <div class="input-group">
            <input type="text" bind:value={receiveCode} placeholder="Enter transfer code..." />
          </div>
          <div class="input-group destination-group">
            <input type="text" bind:value={destinationPath} placeholder="Select destination..." readonly />
            <button class="btn" on:click={selectDestinationAndReceive}>Browse</button>
          </div>
          <button class="btn primary" on:click={receiveFile} disabled={isReceiving || !receiveCode || !destinationPath}>
            {#if isReceiving}
              <div class="spinner"></div>
              <span>Receiving...</span>
            {:else}
              <span>📦 Receive File</span>
            {/if}
          </button>
        </div>
      {/if}
    </div>
  </div>

  <div class="transfers-section">
    <h2>History</h2>
    {#if transfers.length === 0}
      <div class="empty-state">
        <p>🤷‍♀️</p>
        <p>No transfers yet. Send or receive a file to get started!</p>
      </div>
    {:else}
      <div class="transfer-list">
        {#each transfers as transfer (transfer.id)}
          {@const statusInfo = getStatusInfo(transfer.status)}
          <div class="transfer-item" style="--status-color: {statusInfo.color}">
            <div class="status-icon">{statusInfo.icon}</div>
            <div class="transfer-details">
              <div class="filename">{transfer.name || 'Unknown File'}</div>
              <div class="file-list">
                {#if transfer.files}
                  {#each transfer.files as file}
                    <span>{file}</span>
                  {/each}
                {/if}
              </div>
              <div class="file-size">{formatFileSize(transfer.size)}</div>
              {#if transfer.code}
                <div class="code-container">
                  <span>Code:</span>
                  <strong class="code" on:click={() => copyToClipboard(transfer.code)} on:keydown={(e) => { if (e.key === 'Enter') copyToClipboard(transfer.code); }} role="button" tabindex="0" title="Click to copy">
                    {transfer.code}
                  </strong>
                </div>
              {/if}
            </div>
            <div class="transfer-status">
              <div class="status-text">{transfer.status}</div>
              <div class="progress-bar">
                <div class="progress-fill" style="width: {transfer.progress}%"></div>
              </div>
              <div class="progress-text">{transfer.progress}%</div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</main>

{#if toastMessage}
  <div class="toast" class:success={toastType === 'success'} class:error={toastType === 'error'}>
    {toastMessage}
  </div>
{/if}

<style>
  main {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 2rem;
    gap: 2rem;
    min-height: 100vh;
  }

  .header {
    text-align: center;
  }

  .header h1 {
    font-size: 3rem;
    font-weight: 800;
    color: var(--color-text);
  }

  .header p {
    font-size: 1.125rem;
    color: var(--color-text-dim);
  }

  .card {
    width: 100%;
    max-width: 500px;
    background-color: var(--color-bg-light);
    border-radius: var(--border-radius);
    border: 1px solid var(--color-border);
    overflow: hidden;
    box-shadow: 0 10px 15px -3px rgba(0,0,0,0.1), 0 4px 6px -2px rgba(0,0,0,0.05);
  }

  .tabs {
    display: flex;
    background-color: var(--color-bg-lighter);
  }

  .tab {
    flex: 1;
    padding: 1rem;
    background: none;
    border: none;
    color: var(--color-text-dim);
    font-size: 1rem;
    font-weight: 600;
    cursor: pointer;
    transition: var(--transition);
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    border-bottom: 2px solid transparent;
  }

  .tab:hover {
    color: var(--color-text);
  }

  .tab.active {
    color: var(--color-primary);
    border-bottom-color: var(--color-primary);
  }

  .tab-content {
    padding: 1.5rem;
  }
  
  .action-section h2 {
    font-size: 1.5rem;
    margin-bottom: 0.5rem;
  }

  .action-section p {
    color: var(--color-text-dim);
    margin-bottom: 1.5rem;
  }

  .input-group {
    margin-bottom: 1rem;
  }

  .input-group input {
    width: 100%;
    padding: 0.75rem 1rem;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--border-radius);
    color: var(--color-text);
    font-size: 1rem;
    transition: var(--transition);
  }

  .input-group input:focus {
    outline: none;
    border-color: var(--color-primary);
    box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.5);
  }
  
  .destination-group {
    display: flex;
    gap: 0.5rem;
  }

  .destination-group input {
    flex: 1;
  }

  .btn {
    padding: 0.75rem 1.5rem;
    border: none;
    border-radius: var(--border-radius);
    cursor: pointer;
    font-size: 1rem;
    font-weight: 600;
    transition: var(--transition);
    background-color: var(--color-bg-lighter);
    color: var(--color-text);
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
  }

  .btn:hover {
    background-color: var(--color-border);
  }
  
  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn.primary {
    background-color: var(--color-primary);
    color: white;
  }

  .btn.primary:hover:not(:disabled) {
    background-color: var(--color-primary-hover);
  }

  .spinner {
    width: 1rem;
    height: 1rem;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  .transfers-section {
    width: 100%;
    max-width: 700px;
  }

  .transfers-section h2 {
    font-size: 1.5rem;
    margin-bottom: 1rem;
    text-align: left;
  }

  .empty-state {
    background-color: var(--color-bg-light);
    border: 2px dashed var(--color-border);
    border-radius: var(--border-radius);
    padding: 2rem;
    text-align: center;
    color: var(--color-text-dim);
  }
  
  .empty-state p:first-child {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .transfer-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .transfer-item {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 1rem;
    background-color: var(--color-bg-light);
    border-radius: var(--border-radius);
    padding: 1rem;
    border-left: 4px solid var(--status-color);
  }

  .status-icon {
    font-size: 1.5rem;
  }

  .transfer-details {
    text-align: left;
  }

  .filename {
    font-weight: 600;
    color: var(--color-text);
  }

  .file-list {
    font-size: 0.875rem;
    color: var(--color-text-dim);
    display: flex;
    flex-direction: column;
  }

  .file-size {
    font-size: 0.875rem;
    color: var(--color-text-dim);
    margin-top: 0.5rem;
  }
  
  .code-container {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.5rem;
    font-size: 0.875rem;
    color: var(--color-text-dim);
  }

  .code {
    font-family: monospace;
    background-color: var(--color-bg);
    padding: 0.25rem 0.5rem;
    border-radius: 0.25rem;
    color: var(--color-primary);
    cursor: pointer;
  }
  
  .code:hover {
    text-decoration: underline;
  }

  .transfer-status {
    text-align: right;
  }

  .status-text {
    font-size: 0.875rem;
    font-weight: 600;
    text-transform: capitalize;
    color: var(--status-color);
  }

  .progress-bar {
    width: 120px;
    height: 6px;
    background-color: var(--color-bg-lighter);
    border-radius: 3px;
    overflow: hidden;
    margin: 0.5rem 0;
  }

  .progress-fill {
    height: 100%;
    background-color: var(--status-color);
    transition: width 0.3s ease;
  }

  .progress-text {
    font-size: 0.75rem;
    color: var(--color-text-dim);
  }

  .toast {
    position: fixed;
    bottom: 2rem;
    left: 50%;
    transform: translateX(-50%);
    background-color: var(--color-primary);
    color: white;
    padding: 1rem 2rem;
    border-radius: var(--border-radius);
    box-shadow: 0 10px 15px -3px rgba(0,0,0,0.1), 0 4px 6px -2px rgba(0,0,0,0.05);
    z-index: 100;
    animation: fade-in-out 3s ease-in-out forwards;
  }

  .toast.success {
    background-color: var(--color-green);
  }

  .toast.error {
    background-color: var(--color-red);
  }

  @keyframes fade-in-out {
    0% {
      opacity: 0;
      transform: translate(-50%, 20px);
    }
    10% {
      opacity: 1;
      transform: translate(-50%, 0);
    }
    90% {
      opacity: 1;
      transform: translate(-50%, 0);
    }
    100% {
      opacity: 0;
      transform: translate(-50%, 20px);
    }
  }
</style>
