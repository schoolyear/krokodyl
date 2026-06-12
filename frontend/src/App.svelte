<script lang="ts">
  import { onMount } from 'svelte';
  // I18n imports
  import { _, locale } from 'svelte-i18n';
  import { setupi18n } from './i18n';
  import TitleBar from './components/TitleBar.svelte';
  import { theme } from './stores/theme';

  // Wails imports
  import { EventsOn, Environment } from '../wailsjs/runtime/runtime.js';
  import { SendFiles, ReceiveFile, GetTransfers, SelectFiles, SelectDirectory, GetDefaultDownloadPath, RespondToOverwrite, CancelTransfer, GetNearbyPeers, SendToPeer, RespondToNearbyOffer, ResendTransfer, ConfirmResend, GetNearbyPrefs, SetNearbyVisible, ClearHistory, GetDeviceName, GetBuildStamp } from '../wailsjs/go/main/App.js';

  // --- State ---
  let isReady = $state(false); // Tracks if i18n is initialized

  interface FileTransfer {
    id: string;
    name: string;
    files: string[];
    size: number;
    progress: number;
    speed: number;
    status: string;
    code?: string;
    peer?: string;
    error?: string;
    resendable?: boolean;
    peerMachineId?: string;
    resumeCode?: string;
  }

  interface NearbyOffer {
    id: string;
    senderName: string;
    senderAddr: string;
    files: string[];
    size: number;
  }

  interface OverwritePrompt {
    promptId: string;
    transferId: string;
    fileName: string;
    oldSize: number;
    newSize: number;
    oldModTime: string;
    newModTime: string;
  }

  interface VerifyPrompt {
    promptId: string;
    transferId: string;
    detail: string;
  }

  interface NearbyPeer {
    id: string;
    name: string;
    addr: string;
    addrs?: string[];
    port?: number;
    machineId?: string;
  }

  let transfers: FileTransfer[] = $state([]);
  let nearbyPeers: NearbyPeer[] = $state([]);
  let discoveryAvailable = $state(true);
  let nearbyOffer: NearbyOffer | null = $state(null);
  let nearbyVisible = $state(true);
  let lastPeerName = $state('');
  let deviceName = $state('');
  let buildStamp = $state('');
  let showClearConfirm = $state(false);
  // Per-row note shown when a "send again" can't proceed (e.g. device gone),
  // so the feedback sits right where the user clicked, not just in a toast.
  let resendNotes: Record<string, string> = $state({});

  // Most recently used device first.
  let sortedPeers = $derived(
    [...nearbyPeers].sort((a, b) =>
      Number(b.name === lastPeerName) - Number(a.name === lastPeerName)
    )
  );
  let receiveCode: string = $state('');
  let destinationPath: string = $state('');
  let activeTab: 'send' | 'receive' = $state('send');
  let isSending = $state(false);
  let isReceiving = $state(false);
  let isDragOver = $state(false);
  let platform = $state('');
  let toastMessage = $state('');
  let toastType: 'success' | 'error' | 'info' = $state('info');
  let toastTimeout: number;
  let overwritePrompt: OverwritePrompt | null = $state(null);
  // A finished nearby receive whose content differs from the accepted offer.
  let verifyPrompt: VerifyPrompt | null = $state(null);
  // A peer resend awaiting the user's check of the target name + address.
  let resendConfirm: { id: string; name: string; addr: string } | null = $state(null);
  // Marks the code input invalid after a failed receive attempt (3.3.1).
  let receiveInvalid = $state(false);

  // The most recent send still waiting for a receiver — its code is the one
  // thing the sender needs right now, so it gets the spotlight.
  let waitingSend = $derived(
    transfers.find(t => t.id.startsWith('send') && t.status === 'waiting' && t.code)
  );

  // Initialize i18n and then render the component
  (async () => {
    await setupi18n();
    isReady = true;
  })();

  // Keep the document language in sync with the chosen locale so screen
  // readers switch TTS voice and pronunciation rules (WCAG 3.1.1).
  $effect(() => {
    if ($locale) document.documentElement.lang = $locale.split('-')[0];
  });

  onMount(() => {
    theme.init();
    const init = async () => {
      try {
        const env = await Environment();
        platform = env.platform;
        // Root class lets global CSS pick material-aware (translucent)
        // surfaces on Windows and traffic-light padding on macOS.
        document.documentElement.classList.add(`platform-${env.platform}`);
      } catch (error) {
        console.error('Could not detect platform', error);
      }
      await loadTransfers();
      try {
        destinationPath = await GetDefaultDownloadPath();
      } catch (error) {
        console.error("Could not get default download path", error);
      }
    };
    init();

    // We must ensure 'isReady' is true before calling any functions that use translations
    const unsubscribe = _.subscribe(async (t: unknown) => {
      if (typeof t !== 'function' || !isReady) return;
      await loadTransfers();
    });

    EventsOn('transfer:updated', (transfer: FileTransfer) => {
      const index = transfers.findIndex(t => t.id === transfer.id);
      if (index !== -1) {
        // $state arrays are deep proxies in Svelte 5: index assignment is
        // reactive on its own, no reassignment needed.
        transfers[index] = transfer;
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
      } else if (transfer.status === 'cancelled') {
        showToast($_('toasts.transfer_cancelled'), 'info');
        if (transfer.id.startsWith('send')) isSending = false;
        if (transfer.id.startsWith('receive')) isReceiving = false;
      }
    });

    EventsOn('transfer:overwrite', (prompt: OverwritePrompt) => {
      overwritePrompt = prompt;
    });

    EventsOn('transfer:verify', (prompt: VerifyPrompt) => {
      verifyPrompt = prompt;
    });

    EventsOn('nearby:updated', (peers: NearbyPeer[]) => {
      nearbyPeers = peers ?? [];
      // A note saying "X isn't nearby" should clear itself the moment X
      // comes back, restoring the Send again button on that row.
      const ids = Object.keys(resendNotes);
      if (ids.length === 0) return;
      const machines = new Set(nearbyPeers.map(p => p.machineId).filter(Boolean));
      const names = new Set(nearbyPeers.map(p => p.name));
      const next: Record<string, string> = {};
      for (const id of ids) {
        const t = transfers.find(tr => tr.id === id);
        const back = t && ((t.peerMachineId && machines.has(t.peerMachineId)) || (t.peer && names.has(t.peer)));
        if (!back) next[id] = resendNotes[id];
      }
      resendNotes = next;
    });

    EventsOn('nearby:state', (state: { available: boolean }) => {
      discoveryAvailable = state.available;
    });

    EventsOn('nearby:offer', (offer: NearbyOffer) => {
      nearbyOffer = offer;
    });

    EventsOn('transfer:cleared', () => {
      transfers = [];
    });

    GetNearbyPeers().then((peers: NearbyPeer[]) => {
      nearbyPeers = peers ?? [];
    }).catch(() => {});

    GetNearbyPrefs().then((prefs: { visible: boolean; lastPeer: string }) => {
      nearbyVisible = prefs.visible;
      lastPeerName = prefs.lastPeer;
    }).catch(() => {});

    GetDeviceName().then((name: string) => {
      deviceName = name;
    }).catch(() => {});

    GetBuildStamp().then((stamp: string) => {
      buildStamp = stamp;
    }).catch(() => {});

    return unsubscribe;
  });

  async function loadTransfers() {
    try {
      transfers = await GetTransfers();
    } catch (error) {
      // Startup race: the Wails bridge may not be ready yet; the
      // transfer:updated events repopulate the list as they arrive.
      console.error('Could not load transfers', error);
    }
  }

  async function browseAndSendFiles() {
    if (isSending) return;
    try {
      const filePaths = await SelectFiles();
      if (filePaths && filePaths.length > 0) {
        showToast($_('toasts.file_selected'), 'info');
        isSending = true;
        await SendFiles(filePaths);
      }
    } catch (error) {
      console.error('Error sending files:', error);
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
      receiveInvalid = !receiveCode.trim();
      showToast($_('toasts.missing_info'), 'error');
      return;
    }
    receiveInvalid = false;

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

  // Auto-receive triggers only on explicit completion signals: a paste
  // (a pasted code is complete by definition) or Enter. Keystroke-based
  // detection cannot work — "0570-infant-chief-ab" already matches the
  // pattern while the user is still typing the last word.
  function tryAutoReceive(raw: string) {
    const code = raw.trim();
    const codeRegex = /^\d{4}(-[a-zA-Z]{2,}){3}$/;
    if (!codeRegex.test(code) || isReceiving) return;
    receiveCode = code;
    receiveFile();
  }

  function handleCodePaste(event: ClipboardEvent) {
    const pasted = event.clipboardData?.getData('text')?.trim() ?? '';
    // Defer so bind:value has applied before we read/submit.
    setTimeout(() => tryAutoReceive(pasted || receiveCode), 0);
  }

  function handleCodeKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') tryAutoReceive(receiveCode);
  }

  async function cancelTransfer(id: string) {
    try {
      await CancelTransfer(id);
      if (id.startsWith('send')) isSending = false;
      if (id.startsWith('receive')) isReceiving = false;
    } catch (error) {
      console.error('Error cancelling transfer:', error);
    }
  }

  function formatFileSize(bytes: number): string {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  const ACTIVE_STATUSES = ['preparing', 'waiting', 'sending', 'receiving', 'reconnecting'];

  function getStatusInfo(status: string): { color: string; icon: string } {
    switch (status) {
      case 'completed': return { color: 'var(--color-green)', icon: '✅' };
      case 'error': return { color: 'var(--color-red)', icon: '❌' };
      case 'cancelled': return { color: 'var(--color-text-dim)', icon: '🚫' };
      case 'waiting': return { color: 'var(--color-yellow)', icon: '⌛' };
      case 'sending':
      case 'receiving': return { color: 'var(--color-primary)', icon: '⏳' };
      case 'reconnecting': return { color: 'var(--color-yellow)', icon: '🔄' };
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

  async function handleOverwriteResponse(response: 'yes' | 'no') {
    if (!overwritePrompt) return;
    // Clear the prompt before awaiting so a double-click or Enter+Escape
    // can't send a second answer; the backend also ignores stale prompt ids.
    const promptId = overwritePrompt.promptId;
    overwritePrompt = null;
    await RespondToOverwrite(promptId, response);
  }

  async function sendToNearbyPeer(peer: NearbyPeer) {
    if (isSending) return;
    try {
      const filePaths = await SelectFiles();
      if (filePaths && filePaths.length > 0) {
        // Same gating as browseAndSendFiles: raised here, cleared by the
        // transfer:updated handler when the transfer goes terminal.
        isSending = true;
        await SendToPeer(peer.id, filePaths);
        lastPeerName = peer.name;
      }
    } catch (error) {
      console.error('Error sending to peer:', error);
      showToast($_('toasts.select_file_failed'), 'error');
      isSending = false;
    }
  }

  async function resendTransfer(id: string) {
    // Clear any previous note on this row before retrying.
    const { [id]: _omit, ...rest } = resendNotes;
    resendNotes = rest;
    try {
      const outcome = await ResendTransfer(id);
      if (outcome?.needsConfirm) {
        // Device identity on the LAN is unauthenticated — the user verifies
        // the target's name and address before any files are offered.
        resendConfirm = { id, name: outcome.peerName ?? '', addr: outcome.peerAddr ?? '' };
        return;
      }
      handleResendOutcome(id, outcome);
    } catch (error) {
      const message = String(error);
      showToast(message, 'error');
      resendNotes = { ...resendNotes, [id]: message };
    }
  }

  function handleResendOutcome(id: string, outcome: { started: boolean; message?: string } | null) {
    if (outcome?.message) {
      showToast(outcome.message, outcome.started ? 'success' : 'error');
      if (!outcome.started) {
        resendNotes = { ...resendNotes, [id]: outcome.message };
      }
    }
  }

  async function handleResendConfirm(confirmed: boolean) {
    if (!resendConfirm) return;
    const id = resendConfirm.id;
    resendConfirm = null;
    if (!confirmed) return;
    try {
      handleResendOutcome(id, await ConfirmResend(id));
    } catch (error) {
      const message = String(error);
      showToast(message, 'error');
      resendNotes = { ...resendNotes, [id]: message };
    }
  }

  async function handleVerifyResponse(keep: boolean) {
    if (!verifyPrompt) return;
    const promptId = verifyPrompt.promptId;
    verifyPrompt = null;
    // Shares the overwrite-response plumbing; stale ids are ignored.
    await RespondToOverwrite(promptId, keep ? 'yes' : 'no');
  }

  async function confirmClearHistory() {
    try {
      await ClearHistory();
    } catch (error) {
      console.error('Error clearing history:', error);
    }
    showClearConfirm = false;
  }

  async function toggleNearbyVisible() {
    nearbyVisible = !nearbyVisible;
    try {
      await SetNearbyVisible(nearbyVisible);
    } catch (error) {
      console.error('Error toggling visibility:', error);
    }
  }

  async function handleOfferResponse(accept: boolean) {
    if (nearbyOffer) {
      await RespondToNearbyOffer(nearbyOffer.id, accept);
      nearbyOffer = null;
    }
  }

  // Arrow-key navigation for the Send/Receive tablist (ARIA tabs pattern).
  // Roving-tabindex tabs: arrow/Home/End move selection and focus together
  // (automatic activation, per the ARIA tabs pattern).
  function handleTabKeydown(event: KeyboardEvent) {
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight' || event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      activeTab = (event.key === 'ArrowRight' || event.key === 'End') ? 'receive' : 'send';
      const id = activeTab === 'send' ? 'tab-send' : 'tab-receive';
      document.getElementById(id)?.focus();
    }
  }

  // Svelte action: make a modal usable by keyboard — focus the first control,
  // trap Tab within it, close on Escape, and restore focus to whatever was
  // focused before it opened.
  function modalDialog(node: HTMLElement, onClose: () => void) {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const focusables = () => Array.from(
      node.querySelectorAll<HTMLElement>('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])')
    ).filter((el) => !el.hasAttribute('disabled'));

    focusables()[0]?.focus();

    function onKeydown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key !== 'Tab') return;
      const items = focusables();
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }

    node.addEventListener('keydown', onKeydown);
    return {
      destroy() {
        node.removeEventListener('keydown', onKeydown);
        previouslyFocused?.focus?.();
      },
    };
  }
</script>

{#if isReady}
  <div class="app-shell">
    <TitleBar {platform} />

    <main class="scroll-area">
    <section class="surface">
      <div class="segmented" role="tablist" aria-label={$_('a11y.tabs')}>
        <button class="segment" class:active={activeTab === 'send'} role="tab" id="tab-send" aria-controls="panel-send" aria-selected={activeTab === 'send'} tabindex={activeTab === 'send' ? 0 : -1} onclick={() => activeTab = 'send'} onkeydown={handleTabKeydown}>
          📤 {$_('tabs.send')}
        </button>
        <button class="segment" class:active={activeTab === 'receive'} role="tab" id="tab-receive" aria-controls="panel-receive" aria-selected={activeTab === 'receive'} tabindex={activeTab === 'receive' ? 0 : -1} onclick={() => activeTab = 'receive'} onkeydown={handleTabKeydown}>
          📥 {$_('tabs.receive')}
        </button>
      </div>

      {#if activeTab === 'send'}
        <div class="panel" id="panel-send" role="tabpanel" aria-labelledby="tab-send">
          {#if waitingSend?.code}
            <div class="code-spotlight">
              <p class="code-label">{$_('send.share_code')}</p>
              <button class="code-chip" onclick={() => waitingSend?.code && copyToClipboard(waitingSend.code)} aria-label={$_('a11y.copy_code', { values: { code: waitingSend?.code ?? '' } })} title={$_('transfer.copy_prompt')}>
                <span class="code-value">{waitingSend.code}</span>
                <span class="code-copy">⧉</span>
              </button>
              <p class="code-hint">{$_('send.code_hint')}</p>
            </div>
          {/if}

          <div class="nearby">
            <div class="nearby-header">
              <p class="nearby-title">
                {$_('nearby.title')}
                {#if deviceName}<span class="nearby-self">· {$_('history.you_are')} {deviceName}</span>{/if}
              </p>
              <button class="visibility-toggle" class:hidden-state={!nearbyVisible} onclick={toggleNearbyVisible} title={nearbyVisible ? $_('nearby.visible') : $_('nearby.hidden')} aria-label={nearbyVisible ? $_('nearby.visible') : $_('nearby.hidden')} aria-pressed={nearbyVisible}>
                {nearbyVisible ? '👁' : '🙈'}
              </button>
            </div>
            {#if !nearbyVisible}
              <p class="nearby-state">{$_('nearby.hidden')}</p>
            {/if}
            {#if !discoveryAvailable}
              <p class="nearby-state warn">{$_('nearby.unavailable')}</p>
            {:else if nearbyPeers.length === 0}
              <p class="nearby-state">{$_('nearby.empty')}</p>
            {:else}
              <ul class="peer-chips">
                {#each sortedPeers as peer (peer.id)}
                  <li>
                    <button class="peer-chip" onclick={() => sendToNearbyPeer(peer)} disabled={isSending} aria-label={$_('a11y.send_to', { values: { name: peer.name } })} title={peer.addr}>
                      <span class="peer-monogram" aria-hidden="true">{peer.name.charAt(0).toUpperCase()}</span>
                      <span class="peer-name">{peer.name}</span>
                      {#if peer.name === lastPeerName}
                        <span class="peer-recent">{$_('nearby.recent')}</span>
                      {/if}
                    </button>
                  </li>
                {/each}
              </ul>
            {/if}
          </div>

          <div
            class="drop-zone"
            class:drag-over={isDragOver}
            ondragover={(e) => { e.preventDefault(); isDragOver = true; }}
            ondragleave={() => isDragOver = false}
            ondrop={() => isDragOver = false}
            role="region"
            aria-labelledby="drop-hint-text"
          >
            <div class="drop-glyph" aria-hidden="true">📂</div>
            <p class="drop-hint" id="drop-hint-text">{$_('send.drop_hint')}</p>
            <p class="drop-or">{$_('send.drop_or')}</p>
            <button class="btn primary" onclick={browseAndSendFiles} disabled={isSending}>
              {#if isSending}
                <div class="spinner"></div>
                <span>{$_('send.button_sending')}</span>
              {:else}
                <span>{$_('send.button_browse')}</span>
              {/if}
            </button>
          </div>
        </div>
      {:else}
        <div class="panel" id="panel-receive" role="tabpanel" aria-labelledby="tab-receive">
          <h2>{$_('receive.title')}</h2>
          <p class="panel-description">{$_('receive.description')}</p>
          <div class="input-group">
            <input class="code-input" type="text" bind:value={receiveCode} oninput={() => receiveInvalid = false} onpaste={handleCodePaste} onkeydown={handleCodeKeydown} placeholder={$_('receive.placeholder_code')} aria-label={$_('receive.placeholder_code')} aria-describedby="code-input-hint" aria-invalid={receiveInvalid} spellcheck="false" autocomplete="off" />
            <span id="code-input-hint" class="sr-only">{$_('a11y.code_hint')}</span>
          </div>
          <div class="input-group destination-group">
            <input type="text" bind:value={destinationPath} placeholder={$_('receive.placeholder_destination')} aria-label={$_('receive.placeholder_destination')} readonly />
            <button class="btn" onclick={selectDestinationAndReceive}>{$_('receive.button_browse')}</button>
          </div>
          <button class="btn primary wide" onclick={receiveFile} disabled={isReceiving || !receiveCode || !destinationPath}>
            {#if isReceiving}
              <div class="spinner"></div>
              <span>{$_('receive.button_receiving')}</span>
            {:else}
              <span>📦 {$_('receive.button_receive')}</span>
            {/if}
          </button>
        </div>
      {/if}
    </section>

    <section class="transfers-section">
      <div class="history-header">
        <h2>{$_('history.title')}</h2>
        {#if transfers.length > 0}
          <button class="history-clear" onclick={() => showClearConfirm = true}>
            🗑 {$_('history.clear')}
          </button>
        {/if}
      </div>
      {#if transfers.length === 0}
        <div class="empty-state">
          <p>🐊</p>
          <p>{$_('history.empty_state')}</p>
        </div>
      {:else}
        <ul class="transfer-list">
          {#each transfers as transfer (transfer.id)}
            {@const statusInfo = getStatusInfo(transfer.status)}
            <li class="transfer-item" style="--status-color: {statusInfo.color}">
              <div class="status-icon" aria-hidden="true">{statusInfo.icon}</div>
              <div class="transfer-details">
                <div class="filename">
                  {transfer.name || $_('transfer.unknown_file')}
                  {#if transfer.peer}
                    <span class="peer-label">{transfer.id.startsWith('send') ? $_('offer.to') : $_('offer.from_label')} {transfer.peer}</span>
                  {/if}
                </div>
                {#if transfer.status === 'error' && transfer.error}
                  <div class="error-text" role="alert">{transfer.error}</div>
                {/if}
                {#if transfer.files && transfer.files.length > 1}
                  <div class="file-list">
                    {#each transfer.files as file}
                      <span>{file}</span>
                    {/each}
                  </div>
                {/if}
                <div class="file-meta">
                  <span>{formatFileSize(transfer.size)}</span>
                  {#if transfer.code && transfer.status === 'waiting'}
                    <button class="code" onclick={() => {if (transfer.code) copyToClipboard(transfer.code)}} aria-label={$_('a11y.copy_code', { values: { code: transfer.code } })} title={$_('transfer.copy_prompt')}>
                      {transfer.code}
                    </button>
                  {/if}
                </div>
              </div>
              <div class="transfer-status">
                <div class="status-text" role="status" aria-live="polite">{$_(`status.${transfer.status}`, { default: transfer.status })}</div>
                <div class="progress-bar" aria-hidden="true">
                  <div class="progress-fill" style="width: {transfer.progress}%"></div>
                </div>
                <div class="progress-text" aria-hidden="true">
                  {transfer.progress}%{transfer.speed > 0 ? ` · ${formatFileSize(transfer.speed)}/s` : ''}
                </div>
                {#if ACTIVE_STATUSES.includes(transfer.status)}
                  <button class="btn cancel-btn" onclick={() => cancelTransfer(transfer.id)}>
                    ✕ {$_('transfer.cancel')}
                  </button>
                {:else if resendNotes[transfer.id]}
                  <div class="resend-note" role="status" aria-live="polite">{resendNotes[transfer.id]}</div>
                {:else if transfer.resendable}
                  <button class="btn resend-btn" onclick={() => resendTransfer(transfer.id)} title={$_('transfer.resend')}>
                    ↻ {$_('transfer.resend')}
                  </button>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    {#if buildStamp}
      <p class="build-stamp">build {buildStamp}</p>
    {/if}
    </main>
  </div>
{:else}
  <div class="loading-state">
    <div class="spinner"></div>
    <p>Loading application...</p>
  </div>
{/if}


{#if toastMessage}
  <div class="toast" class:success={toastType === 'success'} class:error={toastType === 'error'} role={toastType === 'error' ? 'alert' : 'status'} aria-live={toastType === 'error' ? 'assertive' : 'polite'} aria-atomic="true">
    {toastMessage}
  </div>
{/if}

{#if showClearConfirm}
  <div class="modal-backdrop">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="clear-modal-title" use:modalDialog={() => showClearConfirm = false}>
      <h2 id="clear-modal-title">{$_('history.clear')}</h2>
      <p>{$_('history.clear_confirm')}</p>
      <div class="modal-actions">
        <button class="btn" onclick={() => showClearConfirm = false}>{$_('history.cancel')}</button>
        <button class="btn danger" onclick={confirmClearHistory}>{$_('history.clear_yes')}</button>
      </div>
    </div>
  </div>
{/if}

{#if nearbyOffer}
  <div class="modal-backdrop">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="offer-modal-title" use:modalDialog={() => handleOfferResponse(false)}>
      <h2 id="offer-modal-title">{$_('offer.title')}</h2>
      <p>{$_('offer.from', { values: { name: nearbyOffer.senderName }})}</p>
      <div class="file-diff offer-files">
        {#each nearbyOffer.files.slice(0, 8) as file}
          <div><span>{file}</span></div>
        {/each}
        {#if nearbyOffer.files.length > 8}
          <div><span>… +{nearbyOffer.files.length - 8}</span></div>
        {/if}
        <div>
          <strong>{formatFileSize(nearbyOffer.size)}</strong>
          <span class="offer-addr">{nearbyOffer.senderAddr}</span>
        </div>
      </div>
      <div class="modal-actions">
        <button class="btn" onclick={() => handleOfferResponse(false)}>{$_('offer.decline')}</button>
        <button class="btn primary" onclick={() => handleOfferResponse(true)}>{$_('offer.accept')}</button>
      </div>
    </div>
  </div>
{/if}

{#if overwritePrompt}
  <div class="modal-backdrop">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="overwrite-modal-title" use:modalDialog={() => handleOverwriteResponse('no')}>
      <h2 id="overwrite-modal-title">{$_('overwrite.title')}</h2>
      <p>
        {$_('overwrite.prompt', { values: { file: overwritePrompt.fileName }})}
      </p>
      <div class="file-diff">
        <div>
          <strong>{$_('overwrite.current_size')}:</strong>
          <span>{formatFileSize(overwritePrompt.oldSize)}</span>
        </div>
        <div>
          <strong>{$_('overwrite.new_size')}:</strong>
          <span>{formatFileSize(overwritePrompt.newSize)}</span>
        </div>
        <div>
          <strong>{$_('overwrite.current_modified')}:</strong>
          <span>{overwritePrompt.oldModTime}</span>
        </div>
        <div>
          <strong>{$_('overwrite.new_modified')}:</strong>
          <span>{overwritePrompt.newModTime}</span>
        </div>
      </div>
      <div class="modal-actions">
        <button class="btn" onclick={() => handleOverwriteResponse('no')}>{$_('overwrite.no')}</button>
        <button class="btn primary" onclick={() => handleOverwriteResponse('yes')}>{$_('overwrite.yes')}</button>
      </div>
    </div>
  </div>
{/if}

{#if verifyPrompt}
  <div class="modal-backdrop">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="verify-modal-title" use:modalDialog={() => handleVerifyResponse(false)}>
      <h2 id="verify-modal-title">{$_('verify.title')}</h2>
      <p>{$_('verify.prompt')}</p>
      <p class="verify-detail">{verifyPrompt.detail}</p>
      <div class="modal-actions">
        <button class="btn" onclick={() => handleVerifyResponse(false)}>{$_('verify.discard')}</button>
        <button class="btn danger" onclick={() => handleVerifyResponse(true)}>{$_('verify.keep')}</button>
      </div>
    </div>
  </div>
{/if}

{#if resendConfirm}
  <div class="modal-backdrop">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="resend-modal-title" use:modalDialog={() => handleResendConfirm(false)}>
      <h2 id="resend-modal-title">{$_('resend.title')}</h2>
      <p>{$_('resend.prompt', { values: { name: resendConfirm.name, addr: resendConfirm.addr }})}</p>
      <div class="modal-actions">
        <button class="btn" onclick={() => handleResendConfirm(false)}>{$_('resend.cancel')}</button>
        <button class="btn primary" onclick={() => handleResendConfirm(true)}>{$_('resend.confirm')}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  /* App shell: a fixed-height column so the window chrome strip stays put
     and only the content area scrolls — the page scrollbar never runs
     alongside the titlebar. */
  .app-shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }

  .scroll-area {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: clamp(0.75rem, 2vw, 1.5rem);
    gap: clamp(1rem, 2.5vw, 1.75rem);
  }

  /* --- Main surface --- */
  .surface {
    width: 100%;
    max-width: 560px;
    justify-self: center;
    background-color: var(--color-bg-light);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-2);
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .segmented {
    display: flex;
    background-color: var(--color-bg-lighter);
    border-radius: var(--border-radius);
    padding: 0.25rem;
    gap: 0.25rem;
  }

  .segment {
    flex: 1;
    padding: 0.5rem 0.75rem;
    background: none;
    border: none;
    border-radius: var(--radius-sm);
    color: var(--color-text-dim);
    font-size: clamp(0.875rem, 2.5vw, 0.95rem);
    font-weight: 700;
    cursor: pointer;
    transition: var(--transition);
    white-space: nowrap;
  }

  .segment:hover {
    color: var(--color-text);
  }

  .segment.active {
    background-color: var(--color-bg-light);
    color: var(--color-accent-text);
    box-shadow: var(--shadow-1);
  }

  .panel {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    text-align: center;
  }

  .panel h2 {
    font-size: clamp(1.05rem, 3vw, 1.25rem);
  }

  .panel-description {
    color: var(--color-text-dim);
    font-size: clamp(0.8rem, 2.2vw, 0.9rem);
  }

  /* --- Nearby devices --- */
  .nearby {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    text-align: left;
  }

  .nearby-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .nearby-title {
    font-size: 0.8rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--color-text-dim);
  }

  .visibility-toggle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 1.5rem;
    min-height: 1.5rem;
    background: none;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    padding: 0.125rem 0.375rem;
    font-size: 0.85rem;
    cursor: pointer;
    transition: var(--transition);
  }

  .visibility-toggle:hover {
    border-color: var(--color-primary);
  }

  .visibility-toggle.hidden-state {
    opacity: 0.6;
  }

  .peer-recent {
    font-size: 0.65rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--color-accent-text);
    background-color: var(--color-primary-soft);
    padding: 0.1rem 0.375rem;
    border-radius: 999px;
    flex-shrink: 0;
  }

  .nearby-state {
    font-size: clamp(0.75rem, 2.2vw, 0.85rem);
    color: var(--color-text-dim);
  }

  .nearby-state.warn {
    color: var(--color-yellow);
  }

  .peer-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    /* Semantic list, visual chips. */
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .peer-chips li {
    display: inline-flex;
    max-width: 100%;
  }

  .peer-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.375rem 0.75rem 0.375rem 0.375rem;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: 999px;
    max-width: 100%;
    color: var(--color-text);
    font-family: inherit;
    cursor: pointer;
    transition: var(--transition);
  }

  .peer-chip:hover:not(:disabled) {
    border-color: var(--color-primary);
    background-color: var(--color-primary-soft);
    transform: translateY(-1px);
  }

  .peer-chip:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .peer-monogram {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
    background-color: var(--color-primary-soft);
    color: var(--color-accent-text);
    font-weight: 800;
    font-size: 0.8rem;
    flex-shrink: 0;
  }

  .peer-name {
    font-size: clamp(0.8rem, 2.2vw, 0.9rem);
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* --- Drop zone --- */
  .drop-zone {
    --wails-drop-target: drop;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.5rem;
    padding: clamp(1.5rem, 5vw, 2.5rem) 1rem;
    border: 2px dashed var(--color-border);
    border-radius: var(--border-radius);
    background-color: var(--color-bg);
    transition: border-color var(--duration-fast) var(--ease-out),
                background-color var(--duration-fast) var(--ease-out),
                transform var(--duration-fast) var(--ease-out);
  }

  .drop-zone.drag-over {
    border-color: var(--color-primary);
    border-style: solid;
    background-color: var(--color-primary-soft);
    transform: scale(1.01);
  }

  .drop-glyph {
    font-size: 2.25rem;
    opacity: 0.9;
  }

  .drop-hint {
    font-weight: 700;
    font-size: clamp(0.95rem, 2.5vw, 1.05rem);
  }

  .drop-or {
    color: var(--color-text-dim);
    font-size: 0.8rem;
  }

  /* --- Code spotlight --- */
  .code-spotlight {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.5rem;
    padding: 1rem;
    border-radius: var(--border-radius);
    background-color: var(--color-primary-soft);
    border: 1px solid var(--color-primary);
  }

  .code-label {
    font-size: 0.85rem;
    font-weight: 700;
    color: var(--color-text);
  }

  .code-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.625rem 1rem;
    border: none;
    border-radius: var(--border-radius);
    background-color: var(--color-bg);
    color: var(--color-accent-text);
    font-family: var(--font-family-mono);
    font-size: clamp(0.95rem, 2.8vw, 1.15rem);
    font-weight: 700;
    cursor: pointer;
    box-shadow: var(--shadow-1);
    transition: var(--transition);
    word-break: break-all;
  }

  .code-chip:hover {
    transform: translateY(-1px);
    box-shadow: var(--shadow-2);
  }

  .code-copy {
    opacity: 0.7;
  }

  .code-hint {
    font-size: 0.75rem;
    color: var(--color-text-dim);
  }

  /* --- Inputs --- */
  .input-group {
    display: flex;
    gap: 0.5rem;
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

  .code-input {
    font-family: var(--font-family-mono);
    text-align: center;
    letter-spacing: 0.03em;
  }

  .input-group input:focus {
    /* No outline:none — the global :focus-visible ring must survive for
       keyboard users (WCAG 2.4.7); pointer focus shows border+glow only. */
    border-color: var(--color-primary);
    box-shadow: 0 0 0 3px var(--color-primary-soft);
  }

  .destination-group {
    flex-wrap: wrap;
  }

  .destination-group input {
    flex: 1;
    min-width: 200px;
  }

  .destination-group .btn {
    white-space: nowrap;
  }

  /* --- Buttons --- */
  .btn {
    padding: 0.7rem 1rem;
    border: none;
    border-radius: var(--border-radius);
    cursor: pointer;
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    font-weight: 700;
    transition: var(--transition);
    background-color: var(--color-bg-lighter);
    color: var(--color-text);
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    justify-content: center;
    min-height: 44px;
  }

  .btn:hover {
    background-color: var(--color-border);
  }

  .btn:active:not(:disabled) {
    transform: scale(0.98);
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn.primary {
    /* Darker green so white label text clears WCAG AA 4.5:1 contrast. */
    background-color: var(--color-primary-strong);
    color: #fff;
    box-shadow: var(--shadow-1);
  }

  .btn.primary:hover:not(:disabled) {
    background-color: var(--color-primary-strong-hover);
    transform: translateY(-1px);
    box-shadow: var(--shadow-2);
  }

  .btn.wide {
    width: 100%;
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

  /* --- History --- */
  .transfers-section {
    width: 100%;
    max-width: 700px;
    justify-self: center;
    display: flex;
    flex-direction: column;
    min-height: 160px;
    margin-bottom: 1rem;
  }

  .history-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    margin-bottom: 0.625rem;
  }

  .transfers-section h2 {
    font-size: clamp(1rem, 3vw, 1.15rem);
    text-align: left;
  }

  .history-clear {
    min-height: 1.5rem;
    background: none;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    color: var(--color-text-dim);
    font-size: clamp(0.75rem, 2vw, 0.85rem);
    font-weight: 600;
    padding: 0.3rem 0.625rem;
    cursor: pointer;
    transition: var(--transition);
  }

  .history-clear:hover {
    border-color: var(--color-red);
    color: var(--color-red);
  }

  .btn.danger {
    /* Darker red than --color-red: white label clears AA in both themes. */
    background-color: var(--color-danger-strong);
    color: #fff;
  }

  .nearby-self {
    font-weight: 600;
    text-transform: none;
    letter-spacing: 0;
    color: var(--color-accent-text);
  }

  .empty-state {
    background-color: var(--color-bg-light);
    border: 2px dashed var(--color-border);
    border-radius: var(--border-radius);
    padding: 1.25rem;
    text-align: center;
    color: var(--color-text-dim);
  }

  .empty-state p:first-child {
    font-size: clamp(1.5rem, 6vw, 2.25rem);
    margin-bottom: 0.5rem;
    opacity: 0.7;
  }

  .empty-state p:last-child {
    font-size: clamp(0.8rem, 2.2vw, 0.9rem);
  }

  .transfer-list {
    display: flex;
    flex-direction: column;
    gap: 0.625rem;
    max-height: 420px;
    overflow-y: auto;
    /* Semantic list, visual cards. */
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .transfer-item {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 0.75rem;
    background-color: var(--color-bg-light);
    border: 1px solid var(--color-border);
    border-radius: var(--border-radius);
    padding: 0.75rem;
    border-left: 4px solid var(--status-color);
    box-shadow: var(--shadow-1);
    transition: transform var(--duration-fast) var(--ease-out),
                box-shadow var(--duration-fast) var(--ease-out);
  }

  .transfer-item:hover {
    transform: translateY(-1px);
    box-shadow: var(--shadow-2);
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
    min-width: 0;
  }

  .filename {
    font-weight: 700;
    font-size: clamp(0.875rem, 2.5vw, 0.95rem);
    word-break: break-word;
  }

  .peer-label {
    font-size: clamp(0.7rem, 2vw, 0.8rem);
    font-weight: 600;
    color: var(--color-accent-text);
    margin-left: 0.375rem;
  }

  .offer-files div {
    justify-content: flex-start;
  }

  .offer-files div:last-child {
    justify-content: space-between;
    margin-top: 0.375rem;
  }

  .offer-addr {
    color: var(--color-text-dim);
    font-family: var(--font-family-mono);
    font-size: 0.75rem;
  }

  .error-text {
    font-size: clamp(0.7rem, 2vw, 0.8rem);
    color: var(--color-red);
    margin-top: 0.25rem;
    word-break: break-word;
  }

  .file-list {
    font-size: clamp(0.7rem, 2vw, 0.8rem);
    color: var(--color-text-dim);
    display: flex;
    flex-direction: column;
    margin-top: 0.125rem;
  }

  .file-meta {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
    margin-top: 0.25rem;
    font-size: clamp(0.75rem, 2vw, 0.85rem);
    color: var(--color-text-dim);
  }

  .code {
    display: inline-flex;
    align-items: center;
    min-height: 1.5rem;
    font-family: var(--font-family-mono);
    font-weight: 700;
    background-color: var(--color-bg);
    border: none;
    padding: 0.2rem 0.5rem;
    border-radius: var(--radius-sm);
    color: var(--color-accent-text);
    cursor: pointer;
    word-break: break-all;
    font-size: clamp(0.7rem, 2vw, 0.8rem);
  }

  .code:hover {
    text-decoration: underline;
  }

  .transfer-status {
    text-align: right;
  }

  .status-text {
    font-size: clamp(0.75rem, 2vw, 0.85rem);
    font-weight: 700;
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
    font-size: clamp(0.7rem, 2vw, 0.75rem);
    color: var(--color-text-dim);
  }

  .cancel-btn {
    margin-top: 0.25rem;
    padding: 0.25rem 0.5rem;
    min-height: 1.5rem;
    font-size: clamp(0.7rem, 2vw, 0.8rem);
  }

  .resend-btn {
    margin-top: 0.25rem;
    padding: 0.3rem 0.625rem;
    min-height: 1.5rem;
    font-size: clamp(0.72rem, 2vw, 0.82rem);
    font-weight: 700;
    background-color: var(--color-primary-soft);
    color: var(--color-accent-text);
    border: 1px solid var(--color-primary);
  }

  .resend-btn:hover {
    /* Darker green keeps white label text above 4.5:1 on hover. */
    background-color: var(--color-primary-strong);
    color: #fff;
  }

  .resend-note {
    margin-top: 0.25rem;
    max-width: 220px;
    font-size: clamp(0.68rem, 1.8vw, 0.76rem);
    color: var(--color-text-dim);
    text-align: right;
    line-height: 1.35;
  }

  /* --- Toast --- */
  .toast {
    position: fixed;
    bottom: 2rem;
    left: 50%;
    transform: translateX(-50%);
    /* Strong tokens: white toast text clears AA in both themes. */
    background-color: var(--color-primary-strong);
    color: white;
    padding: 0.875rem 1.5rem;
    border-radius: var(--border-radius);
    box-shadow: var(--shadow-2);
    z-index: 100;
    animation: fade-in-out 3s var(--ease-out) forwards;
    max-width: calc(100vw - 4rem);
    font-size: clamp(0.875rem, 2.5vw, 1rem);
    text-align: center;
  }

  .toast.success {
    background-color: var(--color-primary-strong);
  }

  .toast.error {
    background-color: var(--color-danger-strong);
  }

  @keyframes fade-in-out {
    0% { opacity: 0; transform: translate(-50%, 20px); }
    10% { opacity: 1; transform: translate(-50%, 0); }
    90% { opacity: 1; transform: translate(-50%, 0); }
    100% { opacity: 0; transform: translate(-50%, 20px); }
  }

  /* --- Modal --- */
  .modal-backdrop {
    position: fixed;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    background-color: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }

  .modal {
    background-color: var(--color-bg-light);
    padding: 1.5rem;
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-2);
    width: 100%;
    max-width: min(420px, calc(100vw - 2rem));
    text-align: center;
  }

  .modal h2 {
    font-size: clamp(1.1rem, 3.5vw, 1.3rem);
    margin-bottom: 0.75rem;
  }

  .modal p {
    color: var(--color-text-dim);
    margin-bottom: 1rem;
    word-break: break-word;
  }

  .file-diff {
    text-align: left;
    background-color: var(--color-bg);
    padding: 0.75rem;
    border-radius: var(--border-radius);
    border: 1px solid var(--color-border);
  }

  .file-diff div {
    display: flex;
    justify-content: space-between;
    gap: 0.75rem;
    font-size: clamp(0.75rem, 2.2vw, 0.85rem);
  }

  .file-diff div:not(:last-child) {
    margin-bottom: 0.5rem;
  }

  .verify-detail {
    font-family: var(--font-family-mono);
    font-size: 0.8rem;
    color: var(--color-text-dim);
    background-color: var(--color-bg);
    border-radius: var(--radius-sm);
    padding: 0.5rem 0.625rem;
    word-break: break-word;
  }

  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.75rem;
    margin-top: 1.25rem;
  }

  .build-stamp {
    text-align: center;
    font-size: 0.7rem;
    /* No opacity dampening: --color-text-dim already clears AA on its own. */
    color: var(--color-text-dim);
    font-family: var(--font-family-mono);
    margin: 0.25rem 0 0.5rem;
  }

  /* --- Loading --- */
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

  /* --- Small screens --- */
  @media (max-width: 480px) {
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

    .transfer-list {
      max-height: none;
    }
  }

  /* --- Phone-shaped windows --- */
  @media (max-width: 380px) {
    .segment {
      padding: 0.45rem 0.4rem;
      font-size: 0.8rem;
    }

    .surface {
      padding: 0.75rem;
    }

    .drop-zone {
      padding: 1.25rem 0.75rem;
    }

    .drop-glyph {
      font-size: 1.75rem;
    }

    .code-chip {
      font-size: 0.9rem;
      padding: 0.5rem 0.75rem;
    }

    .input-group {
      flex-wrap: wrap;
    }

    .modal {
      padding: 1rem;
    }

    .file-diff div {
      flex-direction: column;
      gap: 0.125rem;
    }

    .transfer-item {
      padding: 0.5rem;
      gap: 0.5rem;
    }
  }

  /* --- Short windows --- */
  @media (max-height: 560px) {
    .scroll-area {
      gap: 0.625rem;
      padding: 0.625rem;
    }

    .surface {
      padding: 0.75rem;
      gap: 0.625rem;
    }

    .panel {
      gap: 0.5rem;
    }

    .drop-zone {
      padding: 1rem 0.75rem;
      gap: 0.375rem;
    }

    .drop-glyph {
      font-size: 1.5rem;
    }

    .code-spotlight {
      padding: 0.625rem;
      gap: 0.375rem;
    }

    .code-hint {
      display: none;
    }

    .transfers-section {
      min-height: 100px;
    }
  }

  /* --- Large displays --- */
  @media (min-width: 1600px) {
    .scroll-area {
      max-width: 1000px;
      align-self: center;
    }

    .surface {
      max-width: 620px;
    }

    .transfers-section {
      max-width: 820px;
    }
  }
</style>
