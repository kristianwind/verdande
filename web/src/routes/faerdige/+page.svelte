<script>
	/**
	 * Færdige: everything that has been closed, newest first.
	 *
	 * Completing a task is one click on a small circle beside a row you were only
	 * reading, and it takes the row off the screen — so the mistake and the evidence
	 * of it leave together. Somebody who clicked the wrong one had no name to search
	 * for and nowhere to look: the project page can show its own closed tasks, but
	 * only if you already know which project it was in.
	 *
	 * The toast that appears on completing offers an undo, which covers the mistake
	 * you notice at once. This covers the one you notice tomorrow.
	 *
	 * Newest first, decided by the server. A record of what has been finished is
	 * read backwards from now, not by a due date the task no longer has.
	 */
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t, tag } from '$lib/i18n.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';

	let loaded = $state(false);

	$effect(() => {
		app.loadTasks({ completed: 'only', limit: 200 }).then(() => (loaded = true));
	});

	// Read back out of the store, so a task reopened from here leaves the list at
	// once rather than on the next load — which is the whole reason to be here.
	let done = $derived(app.tasks.filter((task) => task.completed));

	/**
	 * Grouped by the day it was closed.
	 *
	 * A flat list of two hundred rows says when nothing. "I dag" and "I går" are
	 * how anybody looking for something they just closed would describe it.
	 */
	let days = $derived.by(() => {
		const groups = new Map();
		for (const task of done) {
			const key = (task.completed_at ?? '').slice(0, 10);
			if (!groups.has(key)) groups.set(key, []);
			groups.get(key).push(task);
		}
		return [...groups.entries()].map(([date, tasks]) => ({ date, tasks }));
	});

	function heading(date) {
		if (!date) return t('done.unknownDay');
		const day = new Date(date + 'T00:00:00');
		const today = new Date(new Date().toDateString());
		const diff = Math.round((day - today) / 86400000);
		if (diff === 0) return t('task.today');
		if (diff === -1) return t('task.yesterday');
		return day.toLocaleDateString(tag(), { weekday: 'long', day: 'numeric', month: 'long' });
	}
</script>

<svelte:head><title>{t('done.title')} — verdande</title></svelte:head>

<div class="view">
	<header>
		<h1>{t('done.title')}</h1>
		<p>{t('done.hint')}</p>
	</header>

	{#each days as day (day.date)}
		<section>
			<h2>{heading(day.date)}</h2>
			{#each day.tasks as task (task.id)}
				<TaskRow {task} />
			{/each}
		</section>
	{/each}

	{#if loaded && !done.length}
		<p class="empty">{t('done.empty')}</p>
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

	header p {
		margin: var(--s1) 0 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	section {
		margin-bottom: var(--s5);
	}

	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		padding-bottom: var(--s2);
		border-bottom: 1px solid var(--line);
		margin-bottom: var(--s2);
	}

	.empty {
		font-size: var(--text-sm);
		color: var(--ink-faint);
	}
</style>
