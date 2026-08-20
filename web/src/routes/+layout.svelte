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
	 * Global keys.
	 *
	 * Typing anywhere starts a task, with the letter already in the field. That is
	 * the whole gesture: no button to hit first, no shortcut to remember, and the
	 * thought you had while looking at a list does not have to survive a journey to
	 * find somewhere to put it.
	 *
	 * It costs the single-letter navigation. "t" for today and "u" for upcoming
	 * cannot also be the first letter of "tal med Anders" — one keyboard cannot
	 * serve both, and capturing is the thing done fifty times a day. Navigation
	 * lives in the palette, which does more and is one keystroke away.
	 *
	 * Ignored while a field already has focus, while a task is open — the drawer is
	 * a place you are, not one you pass through — and for anything held with a
	 * modifier, which belongs to the browser and the system.
	 */
	function onkeydown(event) {
		const el = event.target;
		// Only when nothing has focus. The first version asked the narrower question
		// — "is this a text field?" — and so captured a letter typed at a focused
		// select, button or link, which is where that letter belongs. A key pressed
		// at something is meant for it; only a key pressed at the page itself is
		// spare.
		const idle = el === document.body || el === document.documentElement;
		const typing = !idle;

		if ((event.metaKey || event.ctrlKey) && event.key === 'k') {
			event.preventDefault();
			paletteOpen = true;
			return;
		}
		// Folding the sidebar had a button and nothing else, and a rectangle with a
		// line down it is not a word: somebody who wanted the menu out of the way
		// asked for the feature that was already there. ⌘B is where an editor puts
		// it. Works while typing as well — it moves nothing in the field.
		if ((event.metaKey || event.ctrlKey) && (event.key === 'b' || event.key === 'B')) {
			event.preventDefault();
			sidebar.toggle();
			return;
		}
		if (typing || event.metaKey || event.ctrlKey || event.altKey) return;
		if (app.detailId) return;

		// One character, so Tab, Escape, the arrows and the function keys stay
		// themselves. Space is excluded too: a task that begins with a space begins
		// with a mis-hit, and space is what people press to scroll a long list.
		if (event.key.length !== 1 || event.key === ' ') return;

		// Found by a marker rather than by its label. This used to look the field up
		// by aria-label="Ny opgave", which meant the shortcut quietly did nothing for
		// anybody running the interface in English.
		const field = document.querySelector('[data-quickadd]');
		if (!field) return;

		event.preventDefault();
		field.focus();
		field.value += event.key;
		// Svelte binds on input, and setting .value in script does not raise one.
		field.dispatchEvent(new Event('input', { bubbles: true }));
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
					title={(sidebar.collapsed ? t('nav.showSidebar') : t('nav.hideSidebar')) + ' (⌘B)'}
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
			<div class="toast">
				<!-- The message dismisses, the action acts. They were one button before,
				     which is fine while the only thing a toast says is "that failed" —
				     and wrong the moment it offers to undo something, because then a
				     click meaning "yes, put it back" and a click meaning "go away" land
				     in the same place. -->
				<button class="body" onclick={() => app.dismissToast(toast.id)}>
					{toast.message}
				</button>
				{#if toast.action}
					<button
						class="action"
						onclick={() => {
							app.dismissToast(toast.id);
							toast.onaction?.();
						}}>{toast.action}</button
					>
				{/if}
			</div>
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

	.toast .body {
		flex: 1;
		min-width: 0;
		text-align: left;
		color: inherit;
		font: inherit;
	}

	/* Set apart, because it is the one thing in a toast that does something rather
	   than says something. */
	.toast .action {
		flex: none;
		padding: var(--s1) var(--s2);
		margin: calc(var(--s1) * -1) calc(var(--s1) * -1) calc(var(--s1) * -1) 0;
		border-radius: var(--radius-sm);
		font-weight: 560;
		color: var(--accent);
	}

	.toast .action:hover {
		background: var(--surface);
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
		display: flex;
		align-items: center;
		gap: var(--s3);
		background: var(--surface-raised);
		border: 1px solid var(--line-strong);
		/* The red edge said "something went wrong", which was true while that was
		   all a toast could say. An undo is not a failure — it is the app offering
		   to put something back — so the edge follows the message. */
		border-left: 2px solid var(--danger);
		border-radius: var(--radius);
		box-shadow: var(--shadow-lg);
		padding: var(--s3) var(--s4);
		font-size: var(--text-sm);
		color: var(--ink);
		max-width: 420px;
		text-align: left;
	}

	.toast:has(.action) {
		border-left-color: var(--accent);
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
		}
	}
</style>
