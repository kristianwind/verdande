<script>
	/**
	 * The settings surface.
	 *
	 * Several pages rather than one long scroll: the sections have nothing to do with
	 * each other, and a person opening "API-tokens" should not have to walk past
	 * their own password to get there.
	 *
	 * The form chrome lives here as `:global` rules. Scoped styles do not reach a
	 * child route, and the alternative — six copies of the same twelve selectors —
	 * is six places for them to drift apart.
	 */
	import { page } from '$app/stores';
	import { app } from '$lib/stores.svelte.js';

	let { children } = $props();

	// Brugere is only shown to administrators — the API refuses everybody else, and
	// a tab that answers 403 is a promise the interface cannot keep. Hidden rather
	// than disabled: this is not a feature to upsell, it is one that does not
	// apply to you.
	let sections = $derived([
		{ href: '/indstillinger', label: 'Konto' },
		{ href: '/indstillinger/notifikationer', label: 'Notifikationer' },
		{ href: '/indstillinger/integrationer', label: 'Integrationer' },
		{ href: '/indstillinger/ai', label: 'AI' },
		{ href: '/indstillinger/tokens', label: 'API-tokens' },
		...(app.user?.is_admin
			? [
					{ href: '/indstillinger/brugere', label: 'Brugere' },
					{ href: '/indstillinger/historik', label: 'Historik' },
					{ href: '/indstillinger/fejl', label: 'Fejl' }
				]
			: []),
		{ href: '/indstillinger/data', label: 'Data og skabeloner' }
	]);

	let current = $derived($page.url.pathname.replace(/\/$/, '') || '/indstillinger');
</script>

<div class="settings">
	<header>
		<h1>Indstillinger</h1>
	</header>

	<nav aria-label="Indstillinger">
		{#each sections as section (section.href)}
			<a href={section.href} class:active={current === section.href}>{section.label}</a>
		{/each}
	</nav>

	<div class="body">
		{@render children()}
	</div>
</div>

<style>
	.settings {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	h1 {
		font-size: var(--text-2xl);
		margin-bottom: var(--s5);
	}

	/* A scrolling strip rather than a wrapping row: this many labels wrap to two
	   lines on a phone, and a two-line tab bar looks like a mistake. */
	nav {
		display: flex;
		gap: var(--s1);
		overflow-x: auto;
		border-bottom: 1px solid var(--line);
		margin-bottom: var(--s5);
		scrollbar-width: none;
	}

	nav::-webkit-scrollbar {
		display: none;
	}

	nav a {
		flex: none;
		padding: var(--s2) var(--s3);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		text-decoration: none;
		border-bottom: 2px solid transparent;
		margin-bottom: -1px;
		white-space: nowrap;
		transition: color var(--fast) var(--ease);
	}

	nav a:hover {
		color: var(--ink);
	}

	nav a.active {
		color: var(--ink);
		border-bottom-color: var(--accent);
	}

	.body {
		display: flex;
		flex-direction: column;
		gap: var(--s5);
	}

	/* --- shared form chrome, for the child routes ------------------------------- */

	.settings :global(section.panel) {
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius-lg);
		padding: var(--s5);
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.settings :global(.panel > header) {
		display: flex;
		flex-direction: column;
		gap: var(--s1);
	}

	.settings :global(.panel h2) {
		font-size: var(--text-lg);
	}

	.settings :global(.panel .hint) {
		margin: 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
		line-height: 1.5;
	}

	.settings :global(.field) {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	.settings :global(.field > label) {
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	.settings :global(.field input),
	.settings :global(.field select),
	.settings :global(.field textarea) {
		width: 100%;
		padding: var(--s2) var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
		transition: border-color var(--fast) var(--ease);
	}

	.settings :global(.field input:focus),
	.settings :global(.field select:focus),
	.settings :global(.field textarea:focus) {
		border-color: var(--accent);
	}

	/* The field error sits under its own input rather than in a summary at the top:
	   the server answers with a map of field to message, and putting each one where
	   it belongs is the whole point of that shape. */
	.settings :global(.field .error) {
		margin: 0;
		font-size: var(--text-xs);
		color: var(--danger);
	}

	.settings :global(.field input[aria-invalid='true']) {
		border-color: var(--danger);
	}

	.settings :global(.row) {
		display: flex;
		gap: var(--s3);
		align-items: center;
		flex-wrap: wrap;
	}

	.settings :global(button.primary) {
		padding: var(--s2) var(--s4);
		background: var(--accent);
		color: var(--accent-ink);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		font-weight: 500;
		transition: background var(--fast) var(--ease);
	}

	.settings :global(button.primary:hover) {
		background: var(--accent-hover);
	}

	.settings :global(button.primary:disabled) {
		opacity: 0.5;
		cursor: default;
	}

	.settings :global(button.secondary) {
		padding: var(--s2) var(--s4);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		transition:
			color var(--fast) var(--ease),
			border-color var(--fast) var(--ease);
	}

	.settings :global(button.secondary:hover) {
		color: var(--ink);
		border-color: var(--ink-faint);
	}

	.settings :global(button.danger) {
		padding: var(--s2) var(--s4);
		border: 1px solid var(--danger);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--danger);
		transition: background var(--fast) var(--ease);
	}

	.settings :global(button.danger:hover) {
		background: var(--danger-sunken);
	}

	/* Saved-confirmations are deliberately quiet and inline. A toast for "saved"
	   would interrupt to say nothing went wrong. */
	.settings :global(.saved) {
		font-size: var(--text-sm);
		color: var(--accent);
	}

	.settings :global(.list) {
		display: flex;
		flex-direction: column;
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	.settings :global(.list > li) {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3);
		border-bottom: 1px solid var(--line);
	}

	.settings :global(.list > li:last-child) {
		border-bottom: 0;
	}

	.settings :global(.mono) {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}

	.settings :global(.empty) {
		margin: 0;
		font-size: var(--text-sm);
		color: var(--ink-faint);
	}
</style>
