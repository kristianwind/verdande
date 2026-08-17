<script>
	/**
	 * A month grid for one project.
	 *
	 * Weeks start on Monday. That is not a preference — it is ISO 8601 and it is
	 * what every Danish calendar does, and a Sunday-first grid makes a Dane
	 * misread the whole month at a glance.
	 */
	import { app } from '$lib/stores.svelte.js';

	let { onselect } = $props();

	let cursor = $state(startOfMonth(new Date()));

	function startOfMonth(d) {
		return new Date(d.getFullYear(), d.getMonth(), 1);
	}

	function iso(d) {
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
			d.getDate()
		).padStart(2, '0')}`;
	}

	// The grid always runs whole weeks, so it starts on the Monday on or before the
	// first of the month and ends on the Sunday on or after the last.
	let grid = $derived.by(() => {
		const first = startOfMonth(cursor);
		const start = new Date(first);
		// getDay() is Sunday-based; this converts to "days since Monday".
		start.setDate(first.getDate() - ((first.getDay() + 6) % 7));

		const days = [];
		const cell = new Date(start);
		// Six weeks covers every possible month layout, including a 31-day month
		// that begins on a Sunday.
		for (let i = 0; i < 42; i++) {
			days.push(new Date(cell));
			cell.setDate(cell.getDate() + 1);
		}
		return days;
	});

	const todayISO = iso(new Date());

	const tasksOn = (date) =>
		app.tasks.filter((t) => !t.completed && t.due_date === iso(date));

	const monthName = $derived(
		cursor.toLocaleDateString('da-DK', { month: 'long', year: 'numeric' })
	);

	function step(months) {
		cursor = new Date(cursor.getFullYear(), cursor.getMonth() + months, 1);
	}
</script>

<div class="calendar">
	<header>
		<button onclick={() => step(-1)} aria-label="Forrige måned">‹</button>
		<h2>{monthName}</h2>
		<button onclick={() => step(1)} aria-label="Næste måned">›</button>
		<button class="today" onclick={() => (cursor = startOfMonth(new Date()))}>I dag</button>
	</header>

	<div class="weekdays" aria-hidden="true">
		{#each ['man', 'tir', 'ons', 'tor', 'fre', 'lør', 'søn'] as day}
			<span>{day}</span>
		{/each}
	</div>

	<div class="grid">
		{#each grid as date (date.toISOString())}
			{@const tasks = tasksOn(date)}
			{@const outside = date.getMonth() !== cursor.getMonth()}
			<div class="day" class:outside class:today={iso(date) === todayISO}>
				<span class="number">{date.getDate()}</span>

				{#each tasks.slice(0, 3) as task (task.id)}
					<button class="chip" data-priority={task.priority} onclick={() => onselect?.(task)}>
						{task.content}
					</button>
				{/each}

				{#if tasks.length > 3}
					<span class="more">+{tasks.length - 3} mere</span>
				{/if}
			</div>
		{/each}
	</div>
</div>

<style>
	header {
		display: flex;
		align-items: center;
		gap: var(--s2);
		margin-bottom: var(--s4);
	}

	h2 {
		font-size: var(--text-lg);
		text-transform: capitalize;
		min-width: 180px;
	}

	header button {
		width: 28px;
		height: 28px;
		display: grid;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-muted);
		font-size: var(--text-lg);
		line-height: 1;
	}

	header button:hover {
		background: var(--surface);
		color: var(--ink);
	}

	.today {
		width: auto;
		margin-left: auto;
		padding: 0 var(--s3);
		font-size: var(--text-sm);
		border: 1px solid var(--line);
	}

	.weekdays {
		display: grid;
		grid-template-columns: repeat(7, 1fr);
		gap: 1px;
		margin-bottom: var(--s2);
	}

	.weekdays span {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		padding: 0 var(--s2);
	}

	.grid {
		display: grid;
		grid-template-columns: repeat(7, 1fr);
		gap: 1px;
		background: var(--line);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	.day {
		background: var(--ground);
		min-height: 92px;
		padding: var(--s2);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	/* Days from the neighbouring months stay visible but recede — removing them
	   would leave ragged holes at the corners of the grid. */
	.day.outside {
		background: var(--surface-sunken);
	}

	.day.outside .number {
		color: var(--ink-faint);
		opacity: 0.5;
	}

	.number {
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.day.today .number {
		color: var(--accent-ink);
		background: var(--accent);
		border-radius: var(--radius-full);
		width: 18px;
		height: 18px;
		display: grid;
		place-items: center;
		font-weight: 560;
	}

	.chip {
		font-size: var(--text-xs);
		text-align: left;
		padding: 1px var(--s1);
		border-radius: var(--radius-sm);
		background: var(--surface-raised);
		border-left: 2px solid var(--line-strong);
		color: var(--ink);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.chip[data-priority='1'] {
		border-left-color: var(--p1);
	}
	.chip[data-priority='2'] {
		border-left-color: var(--p2);
	}
	.chip[data-priority='3'] {
		border-left-color: var(--p3);
	}

	.chip:hover {
		background: var(--surface);
	}

	.more {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding-left: var(--s1);
	}

	/* On a phone a seven-column grid of 92px cells is unreadable. The cells shrink
	   and the chips give way to a count — the month shape is the useful part at
	   that width, not the titles. */
	@media (max-width: 620px) {
		.day {
			min-height: 56px;
			padding: var(--s1);
		}

		.chip {
			font-size: 0;
			padding: 0;
			height: 4px;
			border-radius: var(--radius-full);
			border-left: none;
			background: var(--line-strong);
		}

		.chip[data-priority='1'] {
			background: var(--p1);
		}
		.chip[data-priority='2'] {
			background: var(--p2);
		}
		.chip[data-priority='3'] {
			background: var(--p3);
		}

		.more {
			font-size: 10px;
		}
	}
</style>
