<script>
	/** Cmd+K: search across everything the user can see. */
	import { api } from '$lib/api.js';
	import { t } from '$lib/i18n.svelte.js';
	import { goto } from '$app/navigation';
	import { app } from '$lib/stores.svelte.js';
	import { humanMessage } from '$lib/api.js';

	let { open = $bindable(false) } = $props();

	let query = $state('');
	let tasks = $state([]);
	let projects = $state([]);
	let notes = $state([]);
	let selected = $state(0);
	// Svaret på et spørgsmål, når nogen har stillet et. Søgningen finder det, der
	// indeholder ordene; det her svarer på spørgsmålet — og de to bor i det samme
	// felt, fordi man ikke skal vide på forhånd, hvilken slags spørgsmål man har.
	let answer = $state(null);
	let asking = $state(false);
	let input;
	let controller = null;
	let timer = null;

	$effect(() => {
		if (open) {
			query = '';
			tasks = [];
			projects = [];
			answer = null;
			selected = 0;
			queueMicrotask(() => input?.focus());
		}
	});

	$effect(() => {
		const value = query.trim();
		clearTimeout(timer);
		controller?.abort();

		if (!value) {
			tasks = [];
			projects = [];
			notes = [];
			answer = null;
			return;
		}
		// Et nyt bogstav gør det forrige svar forældet. Et svar, der bliver
		// stående, mens spørgsmålet ændrer sig, er et svar på noget andet.
		answer = null;
		timer = setTimeout(async () => {
			try {
				// Noter er sit eget endepunkt, så de hentes ved siden af. Uden dem
				// søger ⌘K kun i halvdelen af programmet, hvilket er svært at gætte
				// som bruger: feltet ser ud til at kunne finde alt.
				const [result, found] = await Promise.all([
					api.search(value),
					api.notes({ q: value }).catch(() => ({ notes: [] }))
				]);
				tasks = result.tasks ?? [];
				projects = result.projects ?? [];
				notes = found.notes ?? [];
				selected = 0;
			} catch {
				// A failed search shows nothing rather than an error inside a
				// palette somebody is about to close anyway.
			}
		}, 140);
	});

	let results = $derived([
		...projects.map((p) => ({ kind: 'project', id: p.id, label: p.name })),
		...notes.map((n) => ({ kind: 'note', id: n.id, label: n.title || n.body.slice(0, 60) })),
		...tasks.map((t) => ({
			kind: 'task',
			id: t.id,
			label: t.content,
			project: t.project_id,
			done: t.completed
		}))
	]);

	/**
	 * Spørg om sine egne noter og opgaver.
	 *
	 * Bag en tast frem for automatisk: det koster et kald til en model, og de
	 * fleste ⌘K er nogen, der leder efter en note, de kender navnet på.
	 */
	async function ask() {
		const question = query.trim();
		if (!question || asking) return;
		asking = true;
		answer = null;
		try {
			answer = await api.aiAsk(question);
		} catch (e) {
			answer = { answer: '', error: humanMessage(e), sources: [] };
		} finally {
			asking = false;
		}
	}

	function openSource(source) {
		open = false;
		if (source.kind === 'note') return goto(`/noter?note=${source.id}`);
		app.openDetail(source.id);
	}

	function choose(item) {
		open = false;
		if (item.kind === 'note') return goto(`/noter?note=${item.id}`);
		goto(item.kind === 'project' ? `/projekt/${item.id}` : `/projekt/${item.project}`);
	}

	function onkeydown(event) {
		switch (event.key) {
			case 'Escape':
				open = false;
				break;
			case 'ArrowDown':
				event.preventDefault();
				selected = Math.min(selected + 1, results.length - 1);
				break;
			case 'ArrowUp':
				event.preventDefault();
				selected = Math.max(selected - 1, 0);
				break;
			case 'Enter':
				event.preventDefault();
				// ⌘⏎ spørger, ⏎ åbner det valgte. Den, der leder efter en note, skal
				// ikke vente på en model for at komme hen til den.
				if (event.metaKey || event.ctrlKey) {
					ask();
					break;
				}
				if (results[selected]) choose(results[selected]);
				break;
		}
	}
</script>

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div class="scrim" onclick={() => (open = false)}>
		<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
		<div class="palette" onclick={(e) => e.stopPropagation()} role="dialog" aria-label={t('nav.search')}>
			<input
				bind:this={input}
				bind:value={query}
				{onkeydown}
				placeholder={t('nav.searchLong')}
				aria-label={t('nav.search')}
				autocomplete="off"
				spellcheck="false"
			/>

			{#if query.trim()}
				<div class="askrow">
					<button class="ask" onclick={ask} disabled={asking}>
						{asking ? t('ai.thinking') : t('ai.askThis')}
					</button>
					<kbd>⌘⏎</kbd>
				</div>
			{/if}

			{#if answer}
				<div class="answer">
					{#if answer.error}
						<p class="bad">{answer.error}</p>
					{:else if answer.answer}
						<p>{answer.answer}</p>
						{#if answer.sources?.length}
							<!-- Hvad svaret står på. En model, der siger "det gjorde du
							     den 14.", er kun brugbar, hvis man kan slå op i det, den
							     læste det i. -->
							<p class="sources">
								{t('ai.askSources')}
								{#each answer.sources as source, i (source.kind + source.id)}<button
										class="source"
										onclick={() => openSource(source)}>{source.title}</button
									>{i < answer.sources.length - 1 ? ', ' : ''}{/each}
							</p>
						{/if}
					{:else}
						<p class="bad">{t('ai.askNothing')}</p>
					{/if}
				</div>
			{/if}

			{#if results.length}
				<ul>
					{#each results as item, i (item.kind + item.id)}
						<li>
							<button
								class:selected={i === selected}
								class:done={item.done}
								onclick={() => choose(item)}
								onmouseenter={() => (selected = i)}
							>
								<span class="kind">{t('palette.' + item.kind)}</span>
								<span class="label">{item.label}</span>
							</button>
						</li>
					{/each}
				</ul>
			{:else if query.trim()}
				<p class="empty">{t('nav.noResults')}</p>
			{/if}
		</div>
	</div>
{/if}

<style>
	.scrim {
		position: fixed;
		inset: 0;
		background: rgb(0 0 0 / 0.45);
		display: flex;
		justify-content: center;
	/* Spørgsmålsknappen står under feltet og fylder ikke: de fleste ⌘K er nogen,
	   der leder efter en note, de kender navnet på, og skal ikke forbi en model
	   for at komme derhen. */
	.askrow {
		display: flex;
		align-items: center;
		gap: var(--s2);
		padding: var(--s1) var(--s2);
		border-top: 1px solid var(--line);
	}

	.ask {
		border: none;
		background: none;
		padding: 0;
		font: inherit;
		font-size: var(--text-xs);
		color: var(--accent);
		cursor: pointer;
	}

	.ask:disabled {
		color: var(--ink-faint);
		cursor: default;
	}

	.answer {
		padding: var(--s2);
		border-top: 1px solid var(--line);
		font-size: var(--text-sm);
		line-height: 1.5;
	}

	.answer p {
		margin: 0 0 var(--s1);
	}

	.answer .bad {
		color: var(--ink-muted);
	}

	.answer .sources {
		margin: 0;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.source {
		border: none;
		background: none;
		padding: 0;
		font: inherit;
		color: var(--accent);
		cursor: pointer;
	}

		/* Not centred: a palette pinned near the top does not jump as results
		   appear, and lands where the eye already is after ⌘K. */
		align-items: flex-start;
		padding-top: 12vh;
		z-index: 70;
	}

	.palette {
		width: min(560px, calc(100vw - var(--s6)));
		background: var(--surface-raised);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-lg);
		overflow: hidden;
	}

	input {
		width: 100%;
		padding: var(--s4);
		background: transparent;
		border: 0;
		border-bottom: 1px solid var(--line);
		font-size: var(--text-lg);
		outline: none;
	}

	input::placeholder {
		color: var(--ink-faint);
	}

	ul {
		list-style: none;
		margin: 0;
		padding: var(--s2);
		max-height: 45vh;
		overflow-y: auto;
	}

	li button {
		display: flex;
		align-items: baseline;
		gap: var(--s3);
		width: 100%;
		padding: var(--s2) var(--s3);
		border-radius: var(--radius);
		text-align: left;
		color: var(--ink);
		font-size: var(--text-sm);
	}

	li button.selected {
		background: var(--surface);
	}

	li button.done .label {
		color: var(--ink-faint);
		text-decoration: line-through;
	}

	.kind {
		flex: none;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		width: 52px;
	}

	.label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.empty {
		margin: 0;
		padding: var(--s5);
		text-align: center;
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}
</style>
