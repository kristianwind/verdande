<script>
	/** The shortcuts, listed. Mapped to Todoist's so muscle memory carries over. */
	const groups = [
		{
			title: 'Navigation',
			keys: [
				['q', 'Hop til feltet “tilføj opgave”'],
				['t', 'I dag'],
				['u', 'Kommende'],
				['⌘K / Ctrl+K', 'Søg'],
				['?', 'Denne side']
			]
		},
		{
			title: 'I quick add',
			keys: [
				['#projekt', 'Læg opgaven i et projekt'],
				['@etiket', 'Sæt en etiket'],
				['p1 – p4', 'Prioritet'],
				['i morgen, på fredag, om 3 dage', 'Dato — dansk og engelsk'],
				['kl 10, 14:30, 7pm', 'Klokkeslæt'],
				['Esc', 'Ryd feltet']
			]
		}
	];
</script>

<div class="view">
	<h1>Genveje</h1>
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
