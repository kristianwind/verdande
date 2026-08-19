<script>
	/** Cmd+K: search across everything the user can see. */
	import { api } from '$lib/api.js';
	import { t } from '$lib/i18n.svelte.js';
	import { goto } from '$app/navigation';

	let { open = $bindable(false) } = $props();

	let query = $state('');
	let tasks = $state([]);
	let projects = $state([]);
	let notes = $state([]);
	let selected = $state(0);
	let input;
	let controller = null;
	let timer = null;

	$effect(() => {
		if (open) {
			query = '';
			tasks = [];
			projects = [];
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
			return;
		}
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
