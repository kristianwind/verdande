<script>
	/**
	 * En liste af forslag, man siger ja eller nej til, ét ad gangen.
	 *
	 * Den samme rude to steder — oprydning i indbakken og opgaver ud af en note —
	 * fordi det er den samme handling: en model har foreslået en linje, og et
	 * menneske bestemmer. To ruder ville betyde to svar på "hvad sker der, når
	 * modellen tager fejl", og det spørgsmål har kun ét rigtigt svar.
	 *
	 * Forslaget står i et felt, man kan skrive i, frem for i en etiket. Det er
	 * hele forskellen mellem at tage imod og at bestemme: et forslag, der er tæt
	 * på, bliver rettet på to tegn i stedet for at blive kasseret — og linjen
	 * læses af den samme parser bagefter, uanset om den blev rørt.
	 *
	 * `why` står under linjen, fordi et forkert gæt skal kunne gennemskues. "Fordi
	 * der står 'inden fredag' i noten" er til at være uenig i; en dato, der bare
	 * dukkede op, er ikke.
	 */
	import { t } from '$lib/i18n.svelte.js';
	import { app } from '$lib/stores.svelte.js';
	import { humanMessage } from '$lib/api.js';

	let { title, hint, load, apply, onclose } = $props();

	let state = $state('loading'); // loading | ready | failed
	let error = $state('');
	let rows = $state([]);
	let busy = $state(null);

	$effect(() => {
		let alive = true;
		state = 'loading';
		load()
			.then((r) => {
				if (!alive) return;
				rows = (r?.suggestions ?? []).map((s, i) => ({ ...s, key: i, line: s.line, done: false }));
				state = 'ready';
			})
			.catch((e) => {
				if (!alive) return;
				error = humanMessage(e);
				state = 'failed';
			});
		return () => {
			alive = false;
		};
	});

	async function accept(row) {
		if (busy !== null) return;
		busy = row.key;
		try {
			await apply(row, row.line);
			rows = rows.map((r) => (r.key === row.key ? { ...r, done: true } : r));
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			busy = null;
		}
	}

	function skip(row) {
		rows = rows.filter((r) => r.key !== row.key);
	}

	const left = $derived(rows.filter((r) => !r.done).length);
</script>

<section class="suggest">
	<header>
		<h3>{title}</h3>
		<button class="close" onclick={onclose} aria-label={t('detail.close')}>×</button>
	</header>

	{#if state === 'loading'}
		<p class="hint">{t('ai.thinking')}</p>
	{:else if state === 'failed'}
		<p class="hint bad">{error}</p>
	{:else if !rows.length}
		<p class="hint">{t('ai.nothingToSuggest')}</p>
	{:else}
		{#if hint}<p class="hint">{hint}</p>{/if}
		<ul>
			{#each rows as row (row.key)}
				<li class:done={row.done}>
					<input
						value={row.line}
						oninput={(e) => (row.line = e.currentTarget.value)}
						disabled={row.done}
						aria-label={title}
					/>
					{#if row.why}<p class="why">{row.why}</p>{/if}
					{#if !row.done}
						<div class="row">
							<button class="button" disabled={busy !== null} onclick={() => accept(row)}>
								{t('ai.accept')}
							</button>
							<button class="secondary" onclick={() => skip(row)}>{t('ai.skip')}</button>
						</div>
					{:else}
						<p class="why ok">{t('ai.applied')}</p>
					{/if}
				</li>
			{/each}
		</ul>
		{#if !left}
			<p class="hint">{t('ai.allDone')}</p>
		{/if}
	{/if}
</section>

<style>
	.suggest {
		margin: var(--s2) 0;
		padding: var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		background: var(--surface-raised, var(--surface));
	}

	header {
		display: flex;
		align-items: baseline;
		gap: var(--s2);
	}

	h3 {
		margin: 0 0 var(--s2);
		font-size: var(--text-sm);
	}

	.close {
		margin-left: auto;
		border: none;
		background: none;
		color: var(--ink-faint);
		cursor: pointer;
		font-size: var(--text-md);
		line-height: 1;
	}

	.hint {
		margin: 0 0 var(--s2);
		font-size: var(--text-xs);
		color: var(--ink-muted);
		line-height: 1.5;
	}

	.hint.bad {
		color: var(--danger, var(--ink));
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	li input {
		width: 100%;
		font-size: var(--text-sm);
		padding: var(--s1);
	}

	li.done input {
		opacity: 0.6;
	}

	/* Grunden til forslaget, under det. Mindre end linjen selv: det er baggrund
	   for en beslutning, ikke selve beslutningen. */
	.why {
		margin: 2px 0 var(--s1);
		font-size: var(--text-xs);
		color: var(--ink-faint);
		line-height: 1.4;
	}

	.why.ok {
		color: var(--ok, var(--ink-muted));
	}

	.row {
		display: flex;
		gap: var(--s2);
	}
</style>
