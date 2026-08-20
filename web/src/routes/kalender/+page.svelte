<script>
	/**
	 * Kalender: Google's events and verdande's own dated tasks in one grid.
	 *
	 * The same component Kommende uses, given a second list to draw. Not a second
	 * grid, and the reason is the part that is easy to get subtly wrong: a drop
	 * target has to decide from `dataTransfer.types` alone whether it accepts a
	 * drag, and a second copy of that is a second place for it to be almost right.
	 * A task dragged here reschedules exactly as it does everywhere else, because
	 * it is the same code doing it.
	 *
	 * A month and a week, and no list. The list is Kommende's answer — seven days
	 * of work, in order — and this view is the other question: what does the day
	 * already have in it.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app, calendarView } from '$lib/stores.svelte.js';
	import CalendarView from '$lib/components/CalendarView.svelte';
	import { t } from '$lib/i18n.svelte.js';

	let events = $state([]);
	/** Null until the first answer, so nothing flashes before it is known. */
	let window_ = $state(null);
	let connected = $state(null);
	/** Whether any calendar in the connected account is ticked. */
	let anyShown = $state(false);
	/** The dates the grid is showing, so the notice below can compare against them. */
	let showing = $state(null);

	$effect(() => {
		api
			.getCalendar()
			.then((status) => {
				connected = status.connected;
				anyShown = (status.calendars ?? []).some((c) => c.shown);
			})
			.catch(() => {
				// A calendar that could not be asked about is not a reason to withhold
				// the grid: the tasks in it are verdande's own and do not depend on
				// Google being reachable.
				connected = false;
			});
	});

	/**
	 * Loads the dates a grid is showing, whichever grid it is.
	 *
	 * Both halves at once, because they answer the same question about the same
	 * cells. The limit on the tasks is raised well above the default for the reason
	 * Kommende raises it: a busy month across every project is more than two hundred
	 * rows, and a silently truncated grid is a calendar that quietly lies about a
	 * day being clear.
	 */
	function loadRange({ from, to }) {
		showing = { from, to };
		app.loadTasks({ due_from: from, due_before: to, limit: 500 });
		api
			.calendarEvents(from, to)
			.then((data) => {
				events = data.events;
				window_ = { from: data.from, to: data.to };
			})
			.catch((e) => {
				// The grid still draws, with the tasks in it. Saying so once beats a
				// view that renders half of itself and explains nothing.
				events = [];
				app.toast(humanMessage(e));
			});
	}

	// Whether the grid has been paged past the window verdande keeps a copy of.
	//
	// Said out loud rather than left as an empty month. A calendar showing no events
	// looks exactly like a calendar with no events in it, and the difference between
	// "nothing is booked" and "I have not looked that far" is the whole reason
	// somebody opened this.
	let beyond = $derived(
		connected &&
			window_ &&
			showing &&
			(showing.from < window_.from || showing.to > window_.to)
	);
</script>

<div class="view">
	<header>
		<h1>{t('cal.title')}</h1>
		<div class="views" role="group" aria-label={t('view.mode')}>
			{#each [['week', t('view.week')], ['month', t('view.month')]] as [value, label]}
				<button
					class:active={calendarView.mode === value}
					onclick={() => calendarView.set(value)}
					aria-pressed={calendarView.mode === value}>{label}</button
				>
			{/each}
		</div>
	</header>

	{#if connected === false}
		<!-- Shown once, and quietly. The grid underneath is already useful without a
		     Google account in it — it is verdande's own calendar, which is what this
		     view was before anything was laid over it. -->
		<p class="hint">
			{t('cal.notConnected')}
			<a href="/indstillinger/integrationer">{t('cal.settings')}</a>
		</p>
	{/if}

	<!-- Connected, and nothing ticked. Worth its own sentence rather than an empty
	     grid: "no calendar is chosen" and "nothing is booked" look identical, and
	     only one of them is something to do about. -->
	{#if connected && !anyShown}
		<p class="hint">
			{t('cal.noneChosen')}
			<a href="/indstillinger/integrationer">{t('cal.settings')}</a>
		</p>
	{/if}

	{#if beyond}
		<p class="hint">{t('cal.beyondWindow', { from: window_.from, to: window_.to })}</p>
	{/if}

	{#if calendarView.mode === 'week'}
		<CalendarView span="week" {events} onrange={loadRange} />
	{:else}
		<CalendarView {events} onrange={loadRange} />
	{/if}
</div>

<style>
	/* A month grid needs the width a reading column deliberately withholds — the
	   same allowance Kommende and the project page make for theirs. */
	.view {
		max-width: 1400px;
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
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

	.hint {
		margin: 0 0 var(--s4);
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	.hint a {
		color: var(--accent);
	}
</style>
