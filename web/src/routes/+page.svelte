<script>
	/** I dag: what is overdue, and what is due today. */
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import QuickAdd from '$lib/components/QuickAdd.svelte';

	let loaded = $state(false);

	// The local date, not the server's: "today" is a question about where the
	// person is standing.
	const isoToday = (() => {
		const now = new Date();
		return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(
			now.getDate()
		).padStart(2, '0')}`;
	})();

	async function load() {
		const data = await api.today();
		app.tasks = [...data.overdue, ...data.today];
		loaded = true;
	}

	$effect(() => {
		load();
	});

	// Derived from the store by *date*, not from the ids that came back with the
	// first request. Filtering against a captured list would mean a task added
	// through quick add — which is pushed into the store, not into that list —
	// never appeared until a reload, which is exactly the moment the interface has
	// to feel immediate.
	let liveOverdue = $derived(
		app.tasks.filter((t) => !t.completed && t.due_date && t.due_date < isoToday)
	);
	let liveToday = $derived(app.tasks.filter((t) => !t.completed && t.due_date === isoToday));

	let date = new Date().toLocaleDateString('da-DK', {
		weekday: 'long',
		day: 'numeric',
		month: 'long'
	});
</script>

<div class="view">
	<header>
		<h1>I dag</h1>
		<p>{date}</p>
	</header>

	<QuickAdd />

	{#if liveOverdue.length}
		<section>
			<h2 class="overdue">Forsinket</h2>
			{#each liveOverdue as task (task.id)}
				<TaskRow {task} />
			{/each}
		</section>
	{/if}

	<section>
		{#if liveOverdue.length}
			<h2>I dag</h2>
		{/if}
		{#each liveToday as task (task.id)}
			<TaskRow {task} />
		{/each}

		{#if loaded && !liveToday.length && !liveOverdue.length}
			<!-- An empty Today is the goal, not a failure state. It should feel
			     like finishing, not like something is missing. -->
			<p class="clear">
				<span class="rune" aria-hidden="true">ᚹ</span>
				Ikke mere i dag.
			</p>
		{/if}
	</section>
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
		color: var(--ink-faint);
		font-size: var(--text-sm);
		text-transform: capitalize;
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
	}

	h2.overdue {
		color: var(--p1);
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
