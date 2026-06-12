<script lang="ts">
  import { _ , locale } from 'svelte-i18n';
  import ThemeSwitcher from './ThemeSwitcher.svelte';
  import { supportedLocales } from '../i18n';
  import { WindowMinimise, WindowToggleMaximise, Quit } from '../../wailsjs/runtime/runtime.js';

  let { platform = '' }: { platform?: string } = $props();

  const isWindows = $derived(platform === 'windows');

  function onStripDblClick() {
    WindowToggleMaximise();
  }
</script>

{#if isWindows}
  <!-- Dedicated native chrome strip: the whole strip drags the window and
       double-clicks to maximize/restore; controls sit top-right like a
       standard Windows window. -->
  <div class="chrome-strip" ondblclick={onStripDblClick} role="presentation">
    <span class="chrome-title">krokodyl</span>
    <div class="window-controls">
      <button class="win-btn" onclick={() => WindowMinimise()} aria-label={$_('titlebar.minimize')} title={$_('titlebar.minimize')}>
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><line x1="0" y1="5" x2="10" y2="5" stroke="currentColor" stroke-width="1"/></svg>
      </button>
      <button class="win-btn" onclick={() => WindowToggleMaximise()} aria-label={$_('titlebar.maximize')} title={$_('titlebar.maximize')}>
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><rect x="0.5" y="0.5" width="9" height="9" fill="none" stroke="currentColor" stroke-width="1"/></svg>
      </button>
      <button class="win-btn close" onclick={() => Quit()} aria-label={$_('titlebar.close')} title={$_('titlebar.close')}>
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M0.5 0.5 L9.5 9.5 M9.5 0.5 L0.5 9.5" stroke="currentColor" stroke-width="1"/></svg>
      </button>
    </div>
  </div>
{/if}

<header class="app-bar" class:mac={platform === 'darwin'}>
  <div class="brand">
    <span class="brand-mark">🐊</span>
    <div class="brand-text">
      <h1>krokodyl</h1>
      <p>{$_('app.subtitle')}</p>
    </div>
  </div>

  <div class="controls">
    <ThemeSwitcher />
    <select class="lang-selector" bind:value={$locale} aria-label={$_('a11y.language')}>
      {#each supportedLocales as l}
        <option value={l}>{l.toUpperCase()}</option>
      {/each}
    </select>
  </div>
</header>

<style>
  /* --- Windows chrome strip --- */
  .chrome-strip {
    --wails-draggable: drag;
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 2rem;
    width: 100%;
    padding-left: 0.75rem;
    background-color: var(--color-bg-light);
    border-bottom: 1px solid var(--color-border);
    user-select: none;
  }

  .chrome-title {
    font-size: 0.75rem;
    font-weight: 700;
    color: var(--color-text-dim);
    letter-spacing: 0.02em;
  }

  .window-controls {
    display: flex;
    align-items: stretch;
    height: 100%;
    --wails-draggable: no-drag;
  }

  .win-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 2.875rem;
    height: 100%;
    background: none;
    border: none;
    color: var(--color-text-dim);
    cursor: pointer;
    transition: background-color var(--duration-fast) var(--ease-out),
                color var(--duration-fast) var(--ease-out);
  }

  .win-btn:hover {
    background-color: var(--color-bg-lighter);
    color: var(--color-text);
  }

  .win-btn.close:hover {
    background-color: #e81123; /* Windows close-button red */
    color: #fff;
  }

  /* --- App bar (brand + theme/lang) — all platforms --- */
  .app-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    width: 100%;
    padding: 0.75rem clamp(0.75rem, 2vw, 1.5rem) 0;
  }

  /* macOS hidden-inset titlebar: clear the floating traffic lights. */
  .app-bar.mac {
    padding-top: 2.25rem;
    -webkit-app-region: drag; /* let the brand row drag the window on mac */
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 0.625rem;
    text-align: left;
    min-width: 0;
  }

  .brand-mark {
    font-size: 1.75rem;
    filter: drop-shadow(0 2px 4px rgba(0, 0, 0, 0.2));
  }

  .brand-text h1 {
    font-size: clamp(1.25rem, 3vw, 1.5rem);
    font-weight: 800;
    letter-spacing: -0.02em;
    line-height: 1.1;
  }

  .brand-text p {
    font-size: clamp(0.7rem, 2vw, 0.8rem);
    color: var(--color-text-dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .lang-selector {
    min-height: 1.75rem;
    padding: 0.375rem 0.5rem;
    border-radius: var(--radius-sm);
    border: 1px solid var(--color-border);
    background-color: var(--color-bg-light);
    color: var(--color-text);
    font-size: 0.8rem;
  }

  @media (max-width: 380px) {
    .app-bar {
      flex-wrap: wrap;
      justify-content: center;
      gap: 0.5rem;
    }

    .brand-text p {
      display: none;
    }
  }

  @media (max-height: 560px) {
    .brand-text p {
      display: none;
    }
  }
</style>
