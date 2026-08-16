<script>
	/** Kommende: the next seven days, empty ones included. */
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import QuickAdd from '$lib/components/QuickAdd.svelte';

	let days = $state([]);

	$effect(() => {
		api.upcoming().then((data) => {
			days = data.days;
			app.tasks = data.days.flatMap((d) => d.tasks);
		});
	});

	function heading(date) {
		const day = new Date(date + 'T00:00:00');
		const today = new Date(new Date().toDateString());
		const diff = Math.round((day - today) / 86400000);

		if (diff === 0) return 'I dag';
		if (diff === 1) return 'I morgen';
		return day.toLocaleDateString('da-DK', { weekday: 'long', day: 'numeric', month: 'short' });
	}

	// Reading tasks back out of the store keeps a completed row disappearing
	// immediately rather than on the next load.
	const live = (date) => app.tasks.filter((t) => t.due_date === date && !t.completed);
</script>

<div class="view">
	<header><h1>Kommende</h1></header>

	<QuickAdd />

	{#each days as day (day.date)}
		{@const tasks = live(day.date)}
		<section>
			<h2>{heading(day.date)}</h2>
			{#if tasks.length}
				{#each tasks as task (task.id)}
					<TaskRow {task} />
				{/each}
			{:else}
				<p class="empty">—</p>
			{/if}
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
		padding: 0 var(--s2) var(--s2);
		border-bottom: 1px solid var(--line);
		margin-bottom: var(--s2);
	}

	/* An em dash rather than "ingen opgaver": seven repetitions of a sentence is
	   noise, and the reader only needs to know the day is clear. */
	.empty {
		margin: 0;
		padding: var(--s2);
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}
</style>
