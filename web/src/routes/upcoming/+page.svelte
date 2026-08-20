<script>
	/**
	 * Kommende: the next seven days as a list, one week as a grid, or the month.
	 *
	 * The views ask for different things and none is a subset of the others — seven
	 * days from today, against a chosen week, against six whole weeks around a
	 * month that can be any month — so each loads its own, and switching reloads.
	 * Loading them all up front would mean the list waiting on a month it is not
	 * showing.
	 *
	 * The week is the answer to dragging across a month boundary. A month grid is
	 * anchored to a month, so the two days on either side of its edge sit in
	 * different grids and moving a task between them means paging mid-drag — which
	 * cannot be done, because a drag in flight swallows the click that would page.
	 * A week that straddles the 31st and the 1st has both days in the same row.
	 */
	import { api } from '$lib/api.js';
	import { app, upcomingView } from '$lib/stores.svelte.js';
	import { TASK, startDrag, carries, dragged, accept } from '$lib/dnd.js';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import QuickAdd from '$lib/components/QuickAdd.svelte';
	import CalendarView from '$lib/components/CalendarView.svelte';
	import { t, tag } from '$lib/i18n.svelte.js';

	let days = $state([]);

	$effect(() => {
		if (upcomingView.mode !== 'list') return;
		api.upcoming().then((data) => {
			days = data.days;
			app.tasks = data.days.flatMap((d) => d.tasks);
		});
	});

	/**
	 * Loads the dates a grid is showing, whichever grid it is.
	 *
	 * The limit is raised well above the default: a busy month across every project
	 * is more than two hundred rows, and a silently truncated grid is a calendar
	 * that quietly lies about a day being clear.
	 */
	function loadRange({ from, to }) {
		app.loadTasks({ due_from: from, due_before: to, limit: 500 });
	}

	function heading(date) {
		const day = new Date(date + 'T00:00:00');
		const today = new Date(new Date().toDateString());
		const diff = Math.round((day - today) / 86400000);

		if (diff === 0) return t('task.today');
		if (diff === 1) return t('task.tomorrow');
		return day.toLocaleDateString(tag(), { weekday: 'long', day: 'numeric', month: 'short' });
	}

	// Reading tasks back out of the store keeps a completed row disappearing
	// immediately rather than on the next load. It is also what makes a task
	// dragged from one day to another land in its new section straight away: the
	// day it belongs to is decided here, not by which list the server put it in.
	const live = (date) => app.tasks.filter((t) => t.due_date === date && !t.completed);

	// --- dragging a task onto another day -------------------------------------------

	/** The day section lit up under the pointer. */
	let over = $state(null);

	function onDragOver(event, date) {
		if (!carries(event, TASK)) return;
		accept(event);
		over = date;
	}

	async function onDrop(event, date) {
		event.preventDefault();
		const id = dragged(event, TASK);
		over = null;
		if (!id) return;
		await app.reschedule(id, date);
	}
</script>

<div class="view" class:wide={upcomingView.mode !== 'list'}>
	<header>
		<h1>{t('view.upcoming')}</h1>
		<div class="views" role="group" aria-label={t('view.mode')}>
			{#each [['list', t('view.list')], ['week', t('view.week')], ['calendar', t('view.month')]] as [value, label]}
				<button
					class:active={upcomingView.mode === value}
					onclick={() => upcomingView.set(value)}
					aria-pressed={upcomingView.mode === value}>{label}</button
				>
			{/each}
		</div>
	</header>

	<QuickAdd />

	{#if upcomingView.mode === 'calendar'}
		<CalendarView onrange={loadRange} />
	{:else if upcomingView.mode === 'week'}
		<CalendarView span="week" onrange={loadRange} />
	{:else}
		{#each days as day (day.date)}
			{@const tasks = live(day.date)}
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<section
				class:over={over === day.date}
				ondragover={(e) => onDragOver(e, day.date)}
				ondragleave={() => (over = null)}
				ondrop={(e) => onDrop(e, day.date)}
			>
				<h2>{heading(day.date)}</h2>
				{#if tasks.length}
					{#each tasks as task (task.id)}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="row"
							draggable="true"
							ondragstart={(e) => startDrag(e, TASK, task.id)}
							ondragend={() => (over = null)}
						>
							<TaskRow {task} />
						</div>
					{/each}
				{:else}
					<!-- The empty day is still a section, and still a target: an em dash
					     is a small thing to aim at, so the whole section takes the drop. -->
					<p class="empty">—</p>
				{/if}
			</section>
		{/each}
	{/if}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	/* A month grid needs the width a reading column deliberately withholds — the
	   same allowance the project page makes for its board and calendar. */
	.view.wide {
		max-width: 1400px;
	}

	/* Wraps, for the same reason the project header does: a title and a switcher
	   are more than a phone's width, and the switcher belongs underneath rather
	   than squeezing the heading. */
	header {
		display: flex;
		align-items: center;
		gap: var(--s3);
		flex-wrap: wrap;
		margin-bottom: var(--s5);
	}

	h1 {
		font-size: var(--text-2xl);
		flex: 1 1 8ch;
		min-width: 8ch;
	}

	.views {
		display: flex;
		gap: 1px;
		flex: none;
		background: var(--line);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	.views button {
		padding: var(--s1) var(--s3);
		font-size: var(--text-sm);
		background: var(--ground);
		color: var(--ink-muted);
		transition:
			background var(--fast) var(--ease),
			color var(--fast) var(--ease);
	}

	.views button:hover {
		color: var(--ink);
	}

	.views button.active {
		background: var(--surface-raised);
		color: var(--ink);
		font-weight: 500;
	}

	section {
		margin-top: var(--s5);
		border-radius: var(--radius);
	}

	/* The day lights up as a whole. Its heading is the label on a box, and the box
	   is what you are dropping into — not the gap between two rows, which is what a
	   line would mean. */
	section.over {
		box-shadow: 0 0 0 1px var(--accent);
		background: var(--surface-sunken);
	}

	.row[draggable='true'] {
		cursor: grab;
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
