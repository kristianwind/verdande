<script>
	/**
	 * The shortcuts, listed.
	 *
	 * The single-letter navigation this page used to carry — q, t, u — is gone.
	 * Typing anywhere now starts a task, and one keyboard cannot both spell "tal
	 * med Anders" and jump to Today on the same "t". Navigation moved into the
	 * palette, which does more and costs one keystroke.
	 */
	import { t } from '$lib/i18n.svelte.js';

	const groups = $derived([
		{
			title: t('keys.navigation'),
			keys: [
				[t('keys.anyLetter'), t('keys.typeToCapture')],
				['⌘K / Ctrl+K', t('nav.search')]
			]
		},
		{
			title: t('keys.inQuickAdd'),
			keys: [
				['#projekt', t('keys.intoProject')],
				['@etiket', t('keys.setLabel')],
				['p1 – p4', t('keys.priority')],
				// The examples stay in both languages, because the parser takes both
				// and the point of the line is what you can actually type.
				['i morgen, på fredag, om 3 dage', t('keys.date')],
				['kl 10, 14:30, 7pm', t('keys.time')],
				['Esc', t('keys.clear')]
			]
		}
	]);
</script>

<div class="view">
	<h1>{t('keys.title')}</h1>
	{#each groups as group}
		<section>
			<h2>{group.title}</h2>
			<dl>
				{#each group.keys as [key, what]}
					<dt><kbd>{key}</kbd></dt>
					<dd>{what}</dd>
				{/each}
			</dl>
		</section>
	{/each}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}
	h1 {
		font-size: var(--text-2xl);
		margin-bottom: var(--s5);
	}
	section {
		margin-top: var(--s5);
	}
	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		padding-bottom: var(--s2);
		border-bottom: 1px solid var(--line);
	}
	dl {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: var(--s3) var(--s4);
		margin: var(--s4) 0 0;
		align-items: baseline;
	}
	dt {
		margin: 0;
	}
	dd {
		margin: 0;
		color: var(--ink-muted);
		font-size: var(--text-sm);
	}
	kbd {
		font-family: var(--font);
		font-size: var(--text-xs);
		color: var(--ink);
		background: var(--surface-raised);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius-sm);
		padding: 2px var(--s2);
		white-space: nowrap;
	}
</style>
