<script lang="ts">
  import { onMount } from 'svelte';
  // I18n imports
  import { _, locale } from 'svelte-i18n';
  import { setupi18n, supportedLocales } from './i18n';

  // Wails imports
  import { EventsOn } from '../wailsjs/runtime/runtime.js';
  import { SendFile, ReceiveFile, GetTransfers, SelectFile, SelectDirectory, GetDefaultDownloadPath } from '../wailsjs/go/main/App.js';

  // --- State ---
  let isReady = false; // Tracks if i18n is initialized
  
  interface FileTransfer {
    id: string;
    name: string;
    files: string[];
    size: number;
    progress: number;
    status: string;
    code?: string;
  }

  let transfers: FileTransfer[] = [];
  let receiveCode: string = '';
  let destinationPath: string = '';
  let activeTab: 'send' | 'receive' = 'send';
  let isSending = false;
  let isReceiving = false;
  let toastMessage = '';
  let toastType: 'success' | 'error' | 'info' = 'info';
  let toastTimeout: number;

  // Initialize i18n and then render the component
  (async () => {
    await setupi18n();
    isReady = true;
  })();

  onMount(() => {
    const init = async () => {
      await loadTransfers();
      try {
        destinationPath = await GetDefaultDownloadPath();
      } catch (error) {
        console.error("Could not get default download path", error);
      }
    };
    init();

    // We must ensure 'isReady' is true before calling any functions that use translations
    const unsubscribe = _.subscribe(async (t) => {
      if (typeof t !== 'function' || !isReady) return;
      await loadTransfers();
    });

    EventsOn('transfer:updated', (transfer: FileTransfer) => {
      const index = transfers.findIndex(t => t.id === transfer.id);
      if (index !== -1) {
        transfers[index] = transfer;
        transfers = [...transfers];
      } else {
        transfers = [transfer, ...transfers];
      }

      if (transfer.status === 'completed') {
        showToast($_('toasts.transfer_completed'), 'success');
        if (transfer.id.startsWith('send')) isSending = false;
        if (transfer.id.startsWith('receive')) isReceiving = false;
      } else if (transfer.status === 'error') {
        showToast($_('toasts.transfer_failed'), 'error');
        if (transfer.id.startsWith('send')) isSending = false;
        if (transfer.id.startsWith('receive')) isReceiving = false;
      }
    });

    return unsubscribe;
  });

  async function loadTransfers() {
    transfers = await GetTransfers();
  }

  async function selectAndSendFile() {
    if (isSending) return;
    try {
      const filePath = await SelectFile();
      if (filePath) {
        showToast($_('toasts.file_selected'), 'info');
        isSending = true;
        await SendFile(filePath);
      }
    } catch (error) {
      console.error('Error sending file:', error);
      showToast($_('toasts.select_file_failed'), 'error');
      isSending = false;
    }
  }

  async function selectDestinationAndReceive() {
    try {
      const path = await SelectDirectory();
      if (path) {
        destinationPath = path;
        showToast($_('toasts.destination_selected'), 'info');
      }
    } catch (error) {
      console.error('Error selecting directory:', error);
      showToast($_('toasts.select_destination_failed'), 'error');
    }
  }

  async function receiveFile() {
    if (isReceiving || !receiveCode.trim() || !destinationPath.trim()) {
      showToast($_('toasts.missing_info'), 'error');
      return;
    }

    try {
      showToast($_('toasts.download_started'), 'info');
      isReceiving = true;
      await ReceiveFile(receiveCode, destinationPath);
      receiveCode = '';
    } catch (error) {
      console.error('Error receiving file:', error);
      showToast($_('toasts.receive_failed'), 'error');
      isReceiving = false;
    }
  }

  function handleCodeInput(event: Event) {
    const code = (event.target as HTMLInputElement).value;
    const codeRegex = /^\d{4}-([a-zA-Z]+-){2}[a-zA-Z]+$/;
    if (codeRegex.test(code)) {
      receiveCode = code;
      receiveFile();
    }
  }

  function formatFileSize(bytes: number): string {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  function getStatusInfo(status: string): { color: string; icon: string } {
    switch (status) {
      case 'completed': return { color: 'var(--color-green)', icon: '✅' };
      case 'error': return { color: 'var(--color-red)', icon: '❌' };
      case 'waiting': return { color: 'var(--color-yellow)', icon: '⌛' };
      case 'sending':
      case 'receiving': return { color: 'var(--color-primary)', icon: '⏳' };
      case 'preparing': return { color: 'var(--color-yellow)', icon: '⌛' };
      default: return { color: 'var(--color-text-dim)', icon: '❓' };
    }
  }

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text);
    showToast($_('toasts.copied_to_clipboard'), 'success');
  }

  function showToast(message: string, type: 'success' | 'error' | 'info' = 'info') {
    if (toastTimeout) {
      clearTimeout(toastTimeout);
    }
    toastMessage = message;
    toastType = type;
    toastTimeout = window.setTimeout(() => {
      toastMessage = '';
    }, 3000);
  }
</script>

{#if isReady}
  <main>
    <div class="header">
      <h1>{$_('app.title')}</h1>
      <p>{$_('app.subtitle')}</p>
      <!-- Language Selector integrated here -->
      <select class="lang-selector" bind:value={$locale}>
        {#each supportedLocales as l}
          <option value={l}>{l.toUpperCase()}</option>
        {/each}
      </select>
    </div>

    <div class="card">
      <div class="tabs">
        <button class="tab" class:active={activeTab === 'send'} on:click={() => activeTab = 'send'}>
          <span>📤</span> {$_('tabs.send')}
        </button>
        <button class="tab" class:active={activeTab === 'receive'} on:click={() => activeTab = 'receive'}>
          <span>📥</span> {$_('tabs.receive')}
        </button>
      </div>

      <div class="tab-content">
        {#if activeTab === 'send'}
          <div class="action-section">
            <h2>{$_('send.title')}</h2>
            <p>{$_('send.description')}</p>
            <button class="btn primary" on:click={selectAndSendFile} disabled={isSending}>
              {#if isSending}
                <div class="spinner"></div>
                <span>{$_('send.button_sending')}</span>
              {:else}
                <span>📁 {$_('send.button')}</span>
              {/if}
            </button>
          </div>
        {:else}
          <div class="action-section">
            <h2>{$_('receive.title')}</h2>
            <p>{$_('receive.description')}</p>
            <div class="input-group">
              <input type="text" bind:value={receiveCode} on:input={handleCodeInput} placeholder={$_('receive.placeholder_code')} />
            </div>
            <div class="input-group destination-group">
              <input type="text" bind:value={destinationPath} placeholder={$_('receive.placeholder_destination')} readonly />
              <button class="btn" on:click={selectDestinationAndReceive}>{$_('receive.button_browse')}</button>
            </div>
            <button class="btn primary" on:click={receiveFile} disabled={isReceiving || !receiveCode || !destinationPath}>
              {#if isReceiving}
                <div class="spinner"></div>
                <span>{$_('receive.button_receiving')}</span>
              {:else}
                <span>📦 {$_('receive.button_receive')}</span>
              {/if}
            </button>
          </div>
        {/if}
      </div>
    </div>

    <div class="transfers-section">
      <h2>{$_('history.title')}</h2>
      {#if transfers.length === 0}
        <div class="empty-state">
          <p>🤷‍♀️</p>
          <p>{$_('history.empty_state')}</p>
        </div>
      {:else}
        <div class="transfer-list">
          {#each transfers as transfer (transfer.id)}
            {@const statusInfo = getStatusInfo(transfer.status)}
            <div class="transfer-item" style="--status-color: {statusInfo.color}">
              <div class="status-icon">{statusInfo.icon}</div>
              <div class="transfer-details">
                <div class="filename">{transfer.name || $_('transfer.unknown_file')}</div>
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
                    <span>{$_('transfer.code_label')}</span>
                    <strong class="code" on:click={() => copyToClipboard(transfer.code)} on:keydown={(e) => { if (e.key === 'Enter') copyToClipboard(transfer.code); }} role="button" tabindex="0" title={$_('transfer.copy_prompt')}>
                      {transfer.code}
                    </strong>
                  </div>
                {/if}
              </div>
              <div class="transfer-status">
                <div class="status-text">{$_(`status.${transfer.status}`, { default: transfer.status })}</div>
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
{:else}
  <!-- You can place a more sophisticated loading spinner here -->
  <div class="loading-state">
    <div class="spinner"></div>
    <p>Loading application...</p>
  </div>
{/if}


{#if toastMessage}
  <div class="toast" class:success={toastType === 'success'} class:error={toastType === 'error'}>
    {toastMessage}
  </div>
{/if}

<style>
  /* --- Add styles for new elements --- */
  .lang-selector {
    margin-top: 0.5rem;
    padding: 0.375rem;
    border-radius: var(--border-radius);
    border: 1px solid var(--color-border);
    background-color: var(--color-bg-light);
    color: var(--color-text);
    font-size: clamp(0.8rem, 2.2vw, 0.9rem);
    min-width: 70px;
  }

  .loading-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    gap: 1rem;
  }

  .loading-state .spinner {
    width: 2rem;
    height: 2rem;
  }

  /* --- All previous styles remain the same --- */
  main {
    display: grid;
    grid-template-rows: auto auto 1fr;
    align-items: start;
    padding: 0.75rem;
    gap: 1rem;
    min-height: 100vh;
    height: 100vh;
    overflow: hidden;
    transition: var(--transition);
  }

  .header {
    text-align: center;
    padding: 0;
  }

  .header h1 {
    font-size: clamp(1.75rem, 4vw, 2.5rem);
    font-weight: 800;
    color: var(--color-text);
    margin-bottom: 0.25rem;
  }

  .header p {
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    color: var(--color-text-dim);
    margin-bottom: 0.25rem;
  }

  .card {
    width: 100%;
    max-width: min(500px, calc(100vw - 1.5rem));
    background-color: var(--color-bg-light);
    border-radius: var(--border-radius);
    border: 1px solid var(--color-border);
    overflow: hidden;
    box-shadow: 0 10px 15px -3px rgba(0,0,0,0.1), 0 4px 6px -2px rgba(0,0,0,0.05);
    justify-self: center;
  }

  .tabs {
    display: flex;
    background-color: var(--color-bg-lighter);
    overflow-x: auto;
  }

  .tab {
    flex: 1;
    min-width: 120px;
    padding: 0.75rem 0.5rem;
    background: none;
    border: none;
    color: var(--color-text-dim);
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    font-weight: 600;
    cursor: pointer;
    transition: var(--transition);
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.25rem;
    border-bottom: 2px solid transparent;
    white-space: nowrap;
  }

  .tab:hover {
    color: var(--color-text);
  }

  .tab.active {
    color: var(--color-primary);
    border-bottom-color: var(--color-primary);
  }

  .tab-content {
    padding: 0.75rem;
  }

  .action-section h2 {
    font-size: clamp(1.125rem, 3.5vw, 1.375rem);
    margin-bottom: 0.375rem;
  }

  .action-section p {
    color: var(--color-text-dim);
    margin-bottom: 0.75rem;
    font-size: clamp(0.8rem, 2.2vw, 0.9rem);
  }

  .input-group {
    margin-bottom: 0.75rem;
  }

  .input-group input {
    width: 100%;
    padding: 0.75rem 1rem;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--border-radius);
    color: var(--color-text);
    font-size: clamp(0.875rem, 2.5vw, 1rem);
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
    flex-wrap: wrap;
  }

  .destination-group input {
    flex: 1;
    min-width: 200px;
  }

  .destination-group .btn {
    white-space: nowrap;
  }

  .btn {
    padding: 0.75rem 1rem;
    border: none;
    border-radius: var(--border-radius);
    cursor: pointer;
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    font-weight: 600;
    transition: var(--transition);
    background-color: var(--color-bg-lighter);
    color: var(--color-text);
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    text-align: center;
    justify-content: center;
    min-height: 44px; /* Minimum touch target size */
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
    max-width: min(700px, calc(100vw - 1.5rem));
    padding: 0;
    justify-self: center;
    display: flex;
    flex-direction: column;
    min-height: 0; /* Allow shrinking */
    overflow: hidden;
  }

  .transfers-section h2 {
    font-size: clamp(1.125rem, 3.5vw, 1.375rem);
    margin-bottom: 0.75rem;
    text-align: left;
    flex-shrink: 0;
  }

  .empty-state {
    background-color: var(--color-bg-light);
    border: 2px dashed var(--color-border);
    border-radius: var(--border-radius);
    padding: 1rem;
    text-align: center;
    color: var(--color-text-dim);
    flex-shrink: 0;
  }

  .empty-state p:first-child {
    font-size: clamp(1.5rem, 6vw, 2.5rem);
    margin-bottom: 0.5rem;
  }

  .empty-state p:last-child {
    font-size: clamp(0.8rem, 2.2vw, 0.9rem);
  }

  .transfer-list {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    overflow-y: auto;
    min-height: 0;
    flex: 1;
  }

  .transfer-item {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 0.75rem;
    background-color: var(--color-bg-light);
    border-radius: var(--border-radius);
    padding: 0.75rem;
    border-left: 4px solid var(--status-color);
  }

  @media (max-width: 600px) {
    .transfer-item {
      grid-template-columns: auto 1fr;
      gap: 0.5rem;
    }
    
    .transfer-status {
      grid-column: 1 / -1;
      text-align: left;
      margin-top: 0.5rem;
    }
    
    .progress-bar {
      width: 100%;
      max-width: 200px;
    }
  }

  .status-icon {
    font-size: 1.25rem;
  }

  .transfer-details {
    text-align: left;
  }

  .filename {
    font-weight: 600;
    color: var(--color-text);
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    word-break: break-word;
  }

  .file-list {
    font-size: clamp(0.75rem, 2vw, 0.875rem);
    color: var(--color-text-dim);
    display: flex;
    flex-direction: column;
  }

  .file-size {
    font-size: clamp(0.75rem, 2vw, 0.875rem);
    color: var(--color-text-dim);
    margin-top: 0.25rem;
  }

  .code-container {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.5rem;
    font-size: clamp(0.75rem, 2vw, 0.875rem);
    color: var(--color-text-dim);
    flex-wrap: wrap;
  }

  .code {
    font-family: monospace;
    background-color: var(--color-bg);
    padding: 0.25rem 0.5rem;
    border-radius: 0.25rem;
    color: var(--color-primary);
    cursor: pointer;
    word-break: break-all;
    font-size: clamp(0.75rem, 2vw, 0.875rem);
  }

  .code:hover {
    text-decoration: underline;
  }

  .transfer-status {
    text-align: right;
  }

  .status-text {
    font-size: clamp(0.75rem, 2vw, 0.875rem);
    font-weight: 600;
    text-transform: capitalize;
    color: var(--status-color);
  }

  .progress-text {
    font-size: clamp(0.7rem, 2vw, 0.75rem);
    color: var(--color-text-dim);
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
    max-width: calc(100vw - 4rem);
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    text-align: center;
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

  /* Additional responsive improvements */
  @media (max-width: 480px) {
    main {
      padding: 0.5rem;
      gap: 0.75rem;
    }
    
    .header h1 {
      margin-bottom: 0.125rem;
    }
    
    .header p {
      margin-bottom: 0.125rem;
    }
    
    .destination-group {
      flex-direction: column;
    }
    
    .destination-group input {
      min-width: unset;
    }
    
    .toast {
      bottom: 1rem;
      padding: 0.75rem 1rem;
    }
  }

  @media (max-width: 360px) {
    .tab {
      padding: 0.5rem 0.25rem;
      font-size: 0.8rem;
    }
    
    .btn {
      padding: 0.5rem 0.75rem;
      font-size: 0.8rem;
    }
    
    .transfer-item {
      padding: 0.5rem;
      gap: 0.5rem;
    }
  }

  /* Ensure buttons are always visible */
  @media (max-height: 600px) {
    .header h1 {
      font-size: clamp(1.5rem, 4vw, 2rem);
      margin-bottom: 0.125rem;
    }
    
    .header p {
      font-size: clamp(0.75rem, 2vw, 0.875rem);
      margin-bottom: 0.125rem;
    }
    
    .lang-selector {
      margin-top: 0.25rem;
    }
    
    .action-section h2 {
      font-size: clamp(1rem, 3vw, 1.25rem);
      margin-bottom: 0.25rem;
    }
    
    .action-section p {
      font-size: clamp(0.75rem, 2vw, 0.825rem);
      margin-bottom: 0.5rem;
    }
    
    .empty-state {
      padding: 0.75rem;
    }
    
    .empty-state p:first-child {
      font-size: clamp(1.25rem, 5vw, 2rem);
      margin-bottom: 0.25rem;
    }
  }
</style>