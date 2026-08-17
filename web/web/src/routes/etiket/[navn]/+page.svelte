<script>
	/**
	 * Everything carrying one label.
	 *
	 * This runs the filter language rather than a bespoke endpoint: "@indkøb" is
	 * already exactly this query, and having one code path means a label page and
	 * a saved filter can never disagree about what a label selects.
	 */
	import { page } from '$app/stores';
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';

	let loaded = $state(false);
	let name = $derived(decodeURIComponent($page.params.navn ?? ''));

	$effect(() => {
		const label = name;
		if (!label) return;
		loaded = false;
		api
			.previewFilter(`@${label}`)
			.then((result) => {
				app.tasks = result?.tasks ?? [];
			})
			.catch(() => {
				app.tasks = [];
			})
			.finally(() => (loaded = true));
	});

	let open = $derived(app.tasks.filter((t) => !t.completed));
</script>

<div class="view">
	<header><h1>@{name}</h1></header>

	{#each open as task (task.id)}
		<TaskRow {task} />
	{/each}

	{#if loaded && !open.length}
		<p class="clear">
			<span class="rune" aria-hidden="true">ᚹ</span>
			Ingen åbne opgaver med denne etiket.
		</p>
	{/if}
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
