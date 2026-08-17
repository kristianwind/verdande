<script>
	/** A saved filter's results. */
	import { page } from '$app/stores';
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';

	let filter = $state(null);
	let error = $state('');

	let id = $derived($page.params.id);

	$effect(() => {
		const filterID = id;
		if (!filterID) return;

		error = '';
		api
			.listFilters()
			.then(({ filters }) => {
				filter = filters.find((f) => f.id === filterID) ?? null;
				return api.runFilter(filterID);
			})
			.then((result) => {
				app.tasks = result?.tasks ?? [];
			})
			.catch((e) => {
				error = e.fields?.query ?? 'Filteret kunne ikke køres.';
				app.tasks = [];
			});
	});

	let open = $derived(app.tasks.filter((t) => !t.completed));
</script>

<div class="view">
	<header>
		<h1>{filter?.name ?? 'Filter'}</h1>
		{#if filter}
			<code>{filter.query}</code>
		{/if}
	</header>

	{#if error}
		<p class="error">{error}</p>
	{:else}
		{#each open as task (task.id)}
			<TaskRow {task} />
		{/each}

		{#if !open.length}
			<p class="clear">
				<span class="rune" aria-hidden="true">ᚹ</span>
				Ingenting matcher lige nu.
			</p>
		{/if}
	{/if}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}
	header {
		margin-bottom: var(--s5);
	}
	h1 {
		font-size: var(--text-2xl);
	}
	code {
		display: inline-block;
		margin-top: var(--s2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--ink-faint);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius-sm);
		padding: 2px var(--s2);
	}
	.error {
		padding: var(--s4);
		background: var(--danger-sunken);
		color: var(--danger);
		border-radius: var(--radius);
		font-size: var(--text-sm);
	}
	.clear {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--s3);
		padding: var(--s8) var(--s4);
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}
	.rune {
		font-size: var(--text-2xl);
		color: var(--accent);
		opacity: 0.5;
	}
</style>
