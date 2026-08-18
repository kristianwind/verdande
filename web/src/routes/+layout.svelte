<script>
	import '../app.css';
	import { app, theme, sidebar } from '$lib/stores.svelte.js';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import CommandPalette from '$lib/components/CommandPalette.svelte';
	import SignIn from '$lib/components/SignIn.svelte';
	import TaskDetail from '$lib/components/TaskDetail.svelte';
	import { t } from '$lib/i18n.svelte.js';

	let { children } = $props();

	let paletteOpen = $state(false);
	let sidebarOpen = $state(false);

	/**
	 * The two pages that arrive by email, and are for somebody who is not signed in.
	 *
	 * They render on their own rather than inside the shell or behind the sign-in
	 * screen. Without this the invite link showed a login form to a person who had
	 * no account yet, and the reset link showed one to a person who had forgotten
	 * the password it was asking for.
	 */
	let standalone = $derived(['/invite', '/reset'].includes($page.url.pathname));

	$effect(() => {
		app.load();
	});

	/**
	 * Global shortcuts, mapped the way Todoist maps them so muscle memory carries
	 * over. They are ignored while a text field has focus — otherwise typing "t"
	 * in a task title would navigate away mid-sentence.
	 */
	function onkeydown(event) {
		const el = event.target;
		const typing =
			el instanceof HTMLInputElement ||
			el instanceof HTMLTextAreaElement ||
			el?.isContentEditable;

		if ((event.metaKey || event.ctrlKey) && event.key === 'k') {
			event.preventDefault();
			paletteOpen = true;
			return;
		}
		if (typing || event.metaKey || event.ctrlKey || event.altKey) return;
		// The navigation shortcuts are for the list. With a task open, "t" means the
		// letter t — the drawer is a place you are, not a place you pass through.
		if (app.detailId) return;

		switch (event.key) {
			case 'q':
				event.preventDefault();
				document.querySelector('input[aria-label="Ny opgave"]')?.focus();
				break;
			case 't':
				goto('/');
				break;
			case 'u':
				goto('/upcoming');
				break;
			case '?':
				goto('/genveje');
				break;
		}
	}
</script>

<svelte:window {onkeydown} />

{#if app.loading}
	<!-- Deliberately blank. A spinner for a request that resolves in 30ms is a
	     flash of anxiety, not feedback. -->
	<div class="booting"></div>
{:else if standalone}
	{@render children()}
{:else if !app.user}
	<SignIn />
{:else}
	<div
		class="shell"
		class:sidebar-open={sidebarOpen}
		class:sidebar-collapsed={sidebar.collapsed}
		style="--sidebar-width: {sidebar.width}px"
	>
		<Sidebar onnavigate={() => (sidebarOpen = false)} />

		<main>
			<header class="topbar">
				<!-- Two controls, not one: on a phone the sidebar is a drawer that
				     opens over the page, on a desktop it is a column that folds away.
				     Same words, different actions, so each is shown only where it
				     applies. -->
				<button
					class="menu"
					onclick={() => (sidebarOpen = !sidebarOpen)}
					aria-label="Vis menu"
					aria-expanded={sidebarOpen}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M4 7h16M4 12h16M4 17h16" />
					</svg>
				</button>

				<button
					class="fold"
					onclick={() => sidebar.toggle()}
					aria-label={sidebar.collapsed ? t('nav.showSidebar') : t('nav.hideSidebar')}
					aria-pressed={sidebar.collapsed}
					title={sidebar.collapsed ? t('nav.showSidebar') : t('nav.hideSidebar')}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<rect x="3" y="4" width="18" height="16" rx="2" />
						<path d="M9 4v16" />
					</svg>
				</button>

				<button class="search" onclick={() => (paletteOpen = true)}>
					<span>{t('nav.search')}</span>
					<kbd>⌘K</kbd>
				</button>

				<button
					class="theme"
					onclick={() => theme.toggle()}
					aria-label={t('nav.toggleTheme')}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z" />
					</svg>
				</button>
			</header>

			<div class="content">
				{@render children()}
			</div>
		</main>

		{#if sidebarOpen}
			<button
				class="scrim"
				onclick={() => (sidebarOpen = false)}
				aria-label={t('nav.closeMenu')}
				tabindex="-1"
			></button>
		{/if}
	</div>

	<CommandPalette bind:open={paletteOpen} />

	<!-- Mounted here rather than in each view, so a task opens the same way from
	     Today, a project, a label or a saved filter. -->
	{#if app.detailTask}
		<TaskDetail task={app.detailTask} onclose={() => app.closeDetail()} />
	{/if}

	<!-- Toasts are for things that failed after the interface already said they
	     had succeeded. Nothing else earns an interruption. -->
	<div class="toasts" role="status" aria-live="polite">
		{#each app.toasts as toast (toast.id)}
			<button class="toast" onclick={() => app.dismissToast(toast.id)}>
				{toast.message}
			</button>
		{/each}
	</div>
{/if}

<style>
	.booting {
		height: 100dvh;
		background: var(--ground);
	}

	.shell {
		display: flex;
		height: 100dvh;
		overflow: hidden;
	}

	main {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		background: var(--ground);
	}

	.topbar {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3) var(--s4);
		border-bottom: 1px solid var(--line);
		flex: none;
		/* Clears the notch on a phone in landscape. */
		padding-left: max(var(--s4), env(safe-area-inset-left));
		padding-right: max(var(--s4), env(safe-area-inset-right));
	}

	.menu {
		display: none;
		width: 32px;
		height: 32px;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-muted);
	}

	.fold {
		display: grid;
		width: 32px;
		height: 32px;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-faint);
		transition: color var(--fast) var(--ease);
	}

	.fold:hover {
		color: var(--ink);
	}

	/* Folded away rather than merely narrow: width 0 with the border gone, so
	   nothing of it is left to catch the eye. */
	.shell.sidebar-collapsed :global(.sidebar) {
		width: 0;
		padding-left: 0;
		padding-right: 0;
		border-right: 0;
		overflow: hidden;
	}

	.menu svg,
	.fold svg,
	.theme svg {
		width: 18px;
		height: 18px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.75;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.search {
		flex: 1;
		max-width: 320px;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--s3);
		padding: var(--s2) var(--s3);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		color: var(--ink-faint);
		font-size: var(--text-sm);
		transition: border-color var(--fast) var(--ease);
	}

	.search:hover {
		border-color: var(--line-strong);
	}

	kbd {
		font-family: var(--font);
		font-size: var(--text-xs);
		color: var(--ink-faint);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius-sm);
		padding: 1px var(--s1);
	}

	.theme {
		margin-left: auto;
		width: 32px;
		height: 32px;
		display: grid;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-muted);
		transition: color var(--fast) var(--ease);
	}

	.theme:hover {
		color: var(--ink);
	}

	.content {
		flex: 1;
		overflow-y: auto;
		overscroll-behavior: contain;
	}

	.scrim {
		display: none;
		position: fixed;
		inset: 0;
		background: rgb(0 0 0 / 0.4);
		border: 0;
		z-index: 40;
	}

	.toasts {
		position: fixed;
		bottom: var(--s4);
		left: 50%;
		transform: translateX(-50%);
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		z-index: 60;
		padding-bottom: env(safe-area-inset-bottom);
	}

	.toast {
		background: var(--surface-raised);
		border: 1px solid var(--line-strong);
		border-left: 2px solid var(--danger);
		border-radius: var(--radius);
		box-shadow: var(--shadow-lg);
		padding: var(--s3) var(--s4);
		font-size: var(--text-sm);
		color: var(--ink);
		max-width: 420px;
		text-align: left;
	}

	/* Below the tablet breakpoint the sidebar becomes a drawer. The layout is
	   mobile-first in behaviour even though the rules read desktop-first: the
	   drawer is the exception, and writing it as one keeps it in a single block. */
	@media (max-width: 820px) {
		.menu {
			display: grid;
		}

		/* The drawer is the whole answer here; folding a drawer means nothing. */
		.fold {
			display: none;
		}

		.scrim {
			display: block;
		}

		.shell :global(.sidebar) {
			position: fixed;
			inset: 0 auto 0 0;
			z-index: 50;
			transform: translateX(-100%);
			transition: transform var(--medium) var(--ease-out);
		}

		.shell.sidebar-open :global(.sidebar) {
			transform: translateX(0);
		}

		/* A collapsed desktop sidebar must not follow you to a phone, where the
		   drawer would then open to nothing. */
		.shell.sidebar-collapsed :global(.sidebar) {
			width: var(--sidebar-width);
			padding-left: max(var(--s3), env(safe-area-inset-left));
			padding-right: var(--s3);
			border-right: 1px solid var(--line);
			overflow-y: auto;
		}
	}
</style>
