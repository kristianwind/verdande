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
	import { goto } from '$app/navigation';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';
	import { findSettings } from '$lib/settingsindex.js';

	let { children } = $props();

	// Brugere is only shown to administrators — the API refuses everybody else, and
	// a tab that answers 403 is a promise the interface cannot keep. Hidden rather
	// than disabled: this is not a feature to upsell, it is one that does not
	// apply to you.
	let sections = $derived([
		{ href: '/indstillinger', label: t('settings.tab.account') },
		{ href: '/indstillinger/notifikationer', label: t('settings.tab.notifications') },
		{ href: '/indstillinger/integrationer', label: t('settings.tab.integrations') },
		{ href: '/indstillinger/ai', label: t('settings.tab.ai') },
		{ href: '/indstillinger/tokens', label: t('settings.tab.tokens') },
		...(app.user?.is_admin
			? [
					{ href: '/indstillinger/brugere', label: t('settings.tab.users') },
					{ href: '/indstillinger/historik', label: t('settings.tab.history') },
					{ href: '/indstillinger/fejl', label: t('settings.tab.errors') }
				]
			: []),
		{ href: '/indstillinger/data', label: t('settings.tab.data') }
	]);

	let current = $derived($page.url.pathname.replace(/\/$/, '') || '/indstillinger');

	/**
	 * Søgningen efter en indstilling.
	 *
	 * Ti faner med en snes afsnit i alt, og den eneste vej til et bestemt af dem
	 * var at huske, hvilken fane det lå under. Feltet her og ⌘K spørger det samme
	 * indeks — se settingsindex.js — så et nyt afsnit kun skal skrives ét sted for
	 * at kunne findes to.
	 */
	let query = $state('');
	let hits = $derived(findSettings(query, { t, isAdmin: app.user?.is_admin ?? false }));

	function go(hit) {
		query = '';
		goto(hit.href);
	}

	function onkeydown(event) {
		if (event.key === 'Escape') {
			query = '';
			return;
		}
		if (event.key === 'Enter' && hits.length) {
			event.preventDefault();
			go(hits[0]);
		}
	}

	/**
	 * Ruller hen til afsnittet, når adressen nævner det, og blinker det.
	 *
	 * Browserens egen springen til et anker kan ikke bruges her: siden hentes af
	 * ruteren, og afsnittet findes først, når dens data er hjemme — så et spring,
	 * der sker med det samme, lander på en side, der endnu er tom. Derfor prøves
	 * det, indtil elementet er der, og gives op efter to sekunder frem for at
	 * prøve for evigt.
	 *
	 * Og blinket er ikke pynt. Man er landet et sted, man ikke selv rullede hen
	 * til, midt i en side med otte afsnit, der ligner hinanden; uden det skal man
	 * læse sig frem til, hvor man er.
	 */
	$effect(() => {
		const anchor = $page.url.hash.slice(1);
		if (!anchor) return;

		let tries = 0;
		let timer;
		const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

		const look = () => {
			const el = document.getElementById(anchor);
			if (!el) {
				if (tries++ < 40) timer = setTimeout(look, 50);
				return;
			}
			el.scrollIntoView({ block: 'start', behavior: reduced ? 'auto' : 'smooth' });
			el.classList.add('landed');
			timer = setTimeout(() => el.classList.remove('landed'), 1600);
		};
		look();

		return () => clearTimeout(timer);
	});
</script>

<div class="settings">
	<header>
		<h1>{t('settings.title')}</h1>
	</header>

	<div class="find">
		<input
			type="search"
			bind:value={query}
			{onkeydown}
			placeholder={t('settings.search')}
			aria-label={t('settings.search')}
			autocomplete="off"
			spellcheck="false"
		/>
		{#if query.trim()}
			{#if hits.length}
				<ul class="hits">
					{#each hits as hit (hit.href)}
						<li>
							<button onclick={() => go(hit)}>
								<span class="where">{hit.tab}</span>
								<span class="what">{hit.title}</span>
							</button>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="nohits">{t('settings.searchNone')}</p>
			{/if}
		{/if}
	</div>

	<nav aria-label={t('settings.title')}>
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

	.find {
		position: relative;
		margin-bottom: var(--s3);
	}

	.find input {
		width: 100%;
		padding: var(--s2) var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
		transition: border-color var(--fast) var(--ease);
	}

	.find input:focus {
		border-color: var(--accent);
	}

	/* Over indholdet frem for at skubbe det ned: listen kommer og går for hvert
	   bogstav, og en side, der hopper under det, man læser, er svær at sigte i. */
	.hits {
		position: absolute;
		z-index: 20;
		left: 0;
		right: 0;
		margin: var(--s1) 0 0;
		padding: var(--s1);
		list-style: none;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		box-shadow: var(--shadow);
	}

	.hits button {
		display: flex;
		align-items: baseline;
		gap: var(--s2);
		width: 100%;
		padding: var(--s2);
		border-radius: var(--radius-sm);
		text-align: left;
		color: var(--ink);
	}

	.hits button:hover {
		background: var(--surface-sunken);
	}

	/* Fanen står før navnet, fordi svaret på "hvor ligger det?" er halvdelen af
	   det, man søgte efter. */
	.where {
		flex: none;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.what {
		font-size: var(--text-sm);
	}

	.nohits {
		margin: var(--s2) 0 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	.settings :global(h3[id]) {
		scroll-margin-top: var(--s5);
	}

	/* Blinket, når man er landet et sted, man ikke selv rullede hen til. Kanten og
	   ikke baggrunden: en baggrund, der skifter, gør teksten svær at læse i netop
	   det øjeblik, man er kommet for at læse den. */
	.settings :global(.landed) {
		animation: landed 1.6s var(--ease);
	}

	@keyframes landed {
		0%,
		60% {
			box-shadow: 0 0 0 2px var(--accent);
		}
		100% {
			box-shadow: 0 0 0 2px transparent;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.settings :global(.landed) {
			animation: none;
		}
	}

	/* --- shared form chrome, for the child routes ------------------------------- */

	.settings :global(section.panel) {
		/* Der er en fast linje øverst på skærmen. Uden den her lander afsnittets
		   overskrift bag den, når man springer hertil fra en søgning. */
		scroll-margin-top: var(--s5);
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
	.settings :global(.field textarea),
	.settings :global(.field output) {
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

	/* Adresserne — feed'et, krogen, CalDAV-serveren — vises i et <output> og ikke i
	   et skrivebeskyttet <input>. Et input kan ikke ombryde: det ruller indeni, så
	   på en telefon står halvdelen af adressen uden for kassen, og der er ingen måde
	   at se resten på. <output> er stadig et felt, en <label for> kan pege på, og det
	   er tekst, der ombryder.

	   `user-select: all` er der, fordi det var det eneste, inputtet gjorde bedre: ét
	   tryk tog hele adressen. Uden den skulle man trække hen over fire linjer. */
	.settings :global(.field output) {
		display: block;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		user-select: all;
		line-height: 1.5;
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
