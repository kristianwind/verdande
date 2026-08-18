<script>
	/**
	 * A calendar grid: a whole month, or a single week.
	 *
	 * Weeks start on Monday. That is not a preference — it is ISO 8601 and it is
	 * what every Danish calendar does, and a Sunday-first grid makes a Dane
	 * misread the whole month at a glance.
	 *
	 * One component for both spans rather than two, because the part that is easy
	 * to get subtly wrong is the same in both: the drag target has to decide from
	 * `dataTransfer.types` alone whether it accepts a drop, and a second copy of
	 * that is a second place for it to be almost right.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { TASK, startDrag, carries, dragged, accept } from '$lib/dnd.js';

	let {
		/**
		 * What a chip does when clicked. Opening the task is what it should do
		 * everywhere, so it is the default rather than something each caller has to
		 * remember — the project page did not, and its chips were dead for as long
		 * as the month view has existed.
		 */
		onselect = (task) => app.openDetail(task.id),
		/**
		 * Told which dates the grid is showing, whenever that changes.
		 *
		 * A project's month view needs nothing: the page has already loaded every
		 * task in the project. Kommende is the other case — it cannot load every
		 * task anybody has ever dated — so it asks for the month on screen, and
		 * this is how it learns which one that is.
		 */
		onrange,
		/**
		 * Narrows the grid to one project. The store holds whatever the last view
		 * loaded, so a project's month has to say which project it is — Kommende,
		 * which shows every project, is the one that leaves this out.
		 */
		projectId = null,
		/**
		 * 'month' for the whole month as six weeks, 'week' for one week as a single
		 * row.
		 *
		 * The week exists for dragging across a month boundary. A month grid is
		 * anchored to a month, so the two days on either side of its edge are in
		 * different grids and moving a task between them means changing month
		 * mid-drag — which the browser will not let you do, because a drag in
		 * flight swallows the click that would page. A week that straddles the
		 * 31st and the 1st has both days in the same row.
		 */
		span = 'month'
	} = $props();

	let cursor = $state(span === 'week' ? startOfWeek(new Date()) : startOfMonth(new Date()));

	const WEEKDAYS = ['man', 'tir', 'ons', 'tor', 'fre', 'lør', 'søn'];

	function startOfMonth(d) {
		return new Date(d.getFullYear(), d.getMonth(), 1);
	}

	// getDay() is Sunday-based; this converts to "days since Monday".
	function startOfWeek(d) {
		const start = new Date(d.getFullYear(), d.getMonth(), d.getDate());
		start.setDate(start.getDate() - ((start.getDay() + 6) % 7));
		return start;
	}

	function startOfSpan(d) {
		return span === 'week' ? startOfWeek(d) : startOfMonth(d);
	}

	// The cursor has to follow the span it is measured in: switching from month to
	// week with a cursor on the 1st would otherwise show the week containing the
	// 1st rather than the week you were looking at, and switching back would land
	// on whichever month that week happened to start in.
	$effect(() => {
		const aligned = startOfSpan(cursor);
		// Compared by value, not identity: assigning a fresh Date on every run would
		// re-trigger this effect forever, since the effect reads what it writes.
		if (aligned.getTime() !== cursor.getTime()) cursor = aligned;
	});

	function iso(d) {
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
			d.getDate()
		).padStart(2, '0')}`;
	}

	// A month always runs whole weeks, so it starts on the Monday on or before the
	// first and ends on the Sunday on or after the last. A week is already one.
	let grid = $derived.by(() => {
		const start = span === 'week' ? startOfWeek(cursor) : startOfWeek(startOfMonth(cursor));
		// Six weeks covers every possible month layout, including a 31-day month
		// that begins on a Sunday.
		const length = span === 'week' ? 7 : 42;

		const days = [];
		const cell = new Date(start);
		for (let i = 0; i < length; i++) {
			days.push(new Date(cell));
			cell.setDate(cell.getDate() + 1);
		}
		return days;
	});

	// Reported after the grid is worked out rather than from the cursor, because
	// the grid runs whole weeks: a month view nearly always shows a few days of the
	// month before it and a few of the one after, and a caller loading only the
	// month itself would leave those corners empty.
	$effect(() => {
		onrange?.({ from: iso(grid[0]), to: iso(grid[grid.length - 1]) });
	});

	const todayISO = iso(new Date());

	const tasksOn = (date) =>
		app.tasks.filter(
			(t) =>
				!t.completed &&
				t.due_date === iso(date) &&
				(!projectId || t.project_id === projectId)
		);

	const monthName = $derived(
		cursor.toLocaleDateString('da-DK', { month: 'long', year: 'numeric' })
	);

	// "24. aug. – 30. aug. 2026", collapsed to one month name when the week does not
	// straddle two. The year is said once, at the end, where it is least in the way.
	const weekName = $derived.by(() => {
		const from = grid[0];
		const to = grid[grid.length - 1];
		const day = { day: 'numeric', month: 'short' };
		const left =
			from.getMonth() === to.getMonth()
				? from.toLocaleDateString('da-DK', { day: 'numeric' })
				: from.toLocaleDateString('da-DK', day);
		return `${left}–${to.toLocaleDateString('da-DK', day)} ${to.getFullYear()}`;
	});

	/**
	 * The ISO week number, which is what a Dane means by "uge 35".
	 *
	 * Counted from the Thursday of the week, because that is the definition: week 1
	 * is the one containing the first Thursday of the year, so the Thursday is the
	 * only day guaranteed to be in the same year as the week it belongs to.
	 */
	function isoWeek(d) {
		const thursday = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()));
		thursday.setUTCDate(thursday.getUTCDate() - ((thursday.getUTCDay() + 6) % 7) + 3);
		const first = new Date(Date.UTC(thursday.getUTCFullYear(), 0, 4));
		first.setUTCDate(first.getUTCDate() - ((first.getUTCDay() + 6) % 7) + 3);
		return 1 + Math.round((thursday - first) / (7 * 86400000));
	}

	const heading = $derived(span === 'week' ? weekName : monthName);
	const weekNumber = $derived(span === 'week' ? isoWeek(grid[0]) : null);

	// A week shows seven cells where a month shows forty-two, so each one has the
	// height to be a list rather than a hint. Truncating it to three there would
	// hide work in the empty half of a tall cell.
	const chipLimit = $derived(span === 'week' ? 10 : 3);

	// One step is one span: a month view pages by month, a week view by week. A week
	// view that paged by month would be back to the problem it exists to solve.
	function step(n) {
		if (span === 'week') {
			const next = new Date(cursor);
			next.setDate(next.getDate() + n * 7);
			cursor = next;
			return;
		}
		cursor = new Date(cursor.getFullYear(), cursor.getMonth() + n, 1);
	}

	// --- dropping a task on a day ---------------------------------------------------

	/** The cell lit up under the pointer. */
	let over = $state(null);

	function onDragOver(event, date) {
		if (!carries(event, TASK)) return;
		accept(event);
		over = iso(date);
	}

	async function onDrop(event, date) {
		event.preventDefault();
		const id = dragged(event, TASK);
		over = null;
		if (!id) return;
		await app.reschedule(id, iso(date));
	}
</script>

<div class="calendar" class:week={span === 'week'}>
	<header>
		<button onclick={() => step(-1)} aria-label={span === 'week' ? 'Forrige uge' : 'Forrige måned'}
			>‹</button
		>
		<h2 class:week={span === 'week'}>
			{heading}
			{#if weekNumber}<span class="weekno">uge {weekNumber}</span>{/if}
		</h2>
		<button onclick={() => step(1)} aria-label={span === 'week' ? 'Næste uge' : 'Næste måned'}
			>›</button
		>
		<button class="today" onclick={() => (cursor = startOfSpan(new Date()))}>I dag</button>
	</header>

	<div class="weekdays" aria-hidden="true">
		{#each WEEKDAYS as day}
			<span>{day}</span>
		{/each}
	</div>

	<div class="grid">
		{#each grid as date (date.toISOString())}
			{@const tasks = tasksOn(date)}
			<!-- A week has no outside: every day in it is the week you asked for. -->
			{@const outside = span !== 'week' && date.getMonth() !== cursor.getMonth()}
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="day"
				data-date={iso(date)}
				class:outside
				class:today={iso(date) === todayISO}
				class:over={over === iso(date)}
				ondragover={(e) => onDragOver(e, date)}
				ondragleave={() => (over = null)}
				ondrop={(e) => onDrop(e, date)}
			>
				<!-- The weekday strip above the grid is enough on a wide screen. A week
				     stacked into rows on a phone hides it, and a column of bare numbers
				     is not a week — so the cell carries its own name there. -->
				{#if span === 'week'}
					<span class="weekday">{WEEKDAYS[(date.getDay() + 6) % 7]}</span>
				{/if}
				<span class="number">{date.getDate()}</span>

				{#each tasks.slice(0, chipLimit) as task (task.id)}
					<!-- Draggable as well as clickable: within a month, moving something
					     to another day is the whole reason to be looking at a month. The
					     browser needs a few pixels of movement before it calls a press a
					     drag, so the click survives. -->
					<button
						class="chip"
						data-priority={task.priority}
						draggable="true"
						ondragstart={(e) => startDrag(e, TASK, task.id)}
						ondragend={() => (over = null)}
						onclick={() => onselect?.(task)}
					>
						{task.content}
					</button>
				{/each}

				{#if tasks.length > chipLimit}
					<span class="more">+{tasks.length - chipLimit} mere</span>
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

	/* A date range is not a month name: "24.–30. aug. 2026" is already capitalised
	   where it should be, and capitalising it again gives "24.–30. Aug. 2026". */
	h2.week {
		text-transform: none;
		min-width: 220px;
		display: flex;
		align-items: baseline;
		gap: var(--s2);
	}

	.weekno {
		font-size: var(--text-xs);
		font-weight: 400;
		color: var(--ink-faint);
		text-transform: uppercase;
		letter-spacing: 0.06em;
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

	/* Scoped to the header, because `.today` is not only this button: the day cell
	   for today carries the same class, and an unscoped `margin-left: auto` made a
	   grid item shrink to its content and sit against the right of its column. The
	   month always had one cell narrower than the other six, on every project
	   calendar, and it read as a grid that had not quite laid out yet. */
	header .today {
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

	/* Seven cells instead of forty-two, so the height a month has to spend on six
	   rows goes into one. It is what makes the week a view rather than only a wider
	   drop target. */
	.calendar.week .day {
		min-height: 320px;
	}

	/* Only shown when the grid has stacked; the strip above says it otherwise. */
	.weekday {
		display: none;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		text-transform: uppercase;
		letter-spacing: 0.06em;
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

	/* The whole cell, because the whole cell is the target: a day is a box, not a
	   gap between two boxes. */
	.day.over {
		background: var(--surface-raised);
		box-shadow: inset 0 0 0 1px var(--accent);
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

		/* A week on a phone is not seven columns of 45px. It becomes seven rows —
		   the same information in the shape the screen has room for, and each row
		   is still a drop target, which is the whole point of the view.

		   The chips stay readable here, unlike in the month grid above: a
		   full-width row has room for a title, and a week somebody opened on
		   purpose is one they want to read rather than count. */
		.calendar.week .grid {
			grid-template-columns: 1fr;
		}

		.calendar.week .day {
			min-height: 0;
			flex-direction: row;
			flex-wrap: wrap;
			align-items: baseline;
			gap: var(--s2);
			padding: var(--s2);
		}

		.calendar.week .weekdays {
			display: none;
		}

		.calendar.week .weekday {
			display: inline;
		}

		.calendar.week .chip {
			font-size: var(--text-xs);
			height: auto;
			padding: 1px var(--s1);
			border-radius: var(--radius-sm);
			border-left: 2px solid var(--line-strong);
			background: var(--surface-raised);
		}

		.calendar.week .chip[data-priority='1'] {
			border-left-color: var(--p1);
			background: var(--surface-raised);
		}
		.calendar.week .chip[data-priority='2'] {
			border-left-color: var(--p2);
			background: var(--surface-raised);
		}
		.calendar.week .chip[data-priority='3'] {
			border-left-color: var(--p3);
			background: var(--surface-raised);
		}

		.calendar.week .more {
			font-size: var(--text-xs);
		}
	}
</style>
