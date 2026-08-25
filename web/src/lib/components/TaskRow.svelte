<script>
	/**
	 * One task.
	 *
	 * The checkbox is the component's whole reason for existing, so it gets the
	 * care: a ring that fills, a checkmark that draws itself, and then the row
	 * fading out. The animation runs to completion locally *before* the row is
	 * removed from the list, because a row that vanishes the instant it is clicked
	 * feels like a mis-click rather than an accomplishment.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { t, tag } from '$lib/i18n.svelte.js';
	import { repeatLabel } from '$lib/when.js';

	let { task, onedit } = $props();

	let repeats = $derived(repeatLabel(task.recurrence_rule, task.recurrence_text));

	let leaving = $state(false);

	const priorityLabel = (p) => (p === 4 ? t('task.noPriority') : t('task.priority', { n: p }));

	async function toggle() {
		if (task.completed) {
			app.reopen(task.id);
			return;
		}
		leaving = true;
		// Matches the fade below. The store's own update is optimistic, so the
		// only thing being waited on here is the animation.
		setTimeout(() => app.complete(task.id), 320);
	}

	function dueLabel(date) {
		if (!date) return null;
		const today = new Date();
		const due = new Date(date + 'T00:00:00');
		const days = Math.round((due - new Date(today.toDateString())) / 86400000);

		if (days === 0) return { text: t('task.today'), tone: 'today' };
		if (days === 1) return { text: t('task.tomorrow'), tone: 'soon' };
		if (days === -1) return { text: t('task.yesterday'), tone: 'overdue' };
		if (days < 0) return { text: t('task.overdue', { n: Math.abs(days) }), tone: 'overdue' };
		if (days < 7)
			return {
				text: due.toLocaleDateString(tag(), { weekday: 'long' }),
				tone: 'soon'
			};
		return {
			text: due.toLocaleDateString(tag(), { day: 'numeric', month: 'short' }),
			tone: 'later'
		};
	}

	// Only when it is somebody else's: see app.assigneeOf.
	let assignee = $derived(app.assigneeOf(task));
	let due = $derived(dueLabel(task.due_date));
	let time = $derived(
		task.due_datetime
			? new Date(task.due_datetime).toLocaleTimeString(tag(), {
					hour: '2-digit',
					minute: '2-digit'
				})
			: null
	);

	// Snoozed while its time is still ahead. Greys the row and adds a "slumret til"
	// mark, so a parked task reads as set-aside rather than forgotten.
	let snoozed = $derived(task.snoozed_until && new Date(task.snoozed_until) > new Date());
	let snoozedLabel = $derived.by(() => {
		if (!snoozed) return null;
		const at = new Date(task.snoozed_until);
		const midnight = new Date();
		midnight.setHours(0, 0, 0, 0);
		const days = Math.round((new Date(at.toDateString()) - midnight) / 86400000);
		const clock = at.toLocaleTimeString(tag(), { hour: '2-digit', minute: '2-digit' });
		if (days <= 0) return clock;
		if (days === 1) return t('task.tomorrow') + ' ' + clock;
		return at.toLocaleDateString(tag(), { day: 'numeric', month: 'short' }) + ' ' + clock;
	});
</script>

<div
	class="row"
	class:leaving
	class:completed={task.completed}
	class:snoozed
	data-priority={task.priority}
>
	<button
		class="check"
		class:checked={task.completed || leaving}
		onclick={toggle}
		aria-label={task.completed ? t('task.reopen') : t('task.complete')}
		title={priorityLabel(task.priority)}
	>
		<svg viewBox="0 0 24 24" aria-hidden="true">
			<path class="tick" d="M6 12.5l4 4 8-8.5" />
		</svg>
	</button>

	<!-- Opening the detail drawer is the default. `onedit` is for the callers that
	     want the click to mean something else. -->
	<button class="body" onclick={() => (onedit ? onedit(task) : app.openDetail(task.id))}>
		<span class="content">{task.content}</span>

		{#if task.description}
			<span class="description">{task.description}</span>
		{/if}

		{#if assignee || due || task.labels?.length || repeats || task.subtask_count || task.attachment_count}
			<span class="meta">
				{#if assignee}
					<!-- First in the row, because "who" changes what the rest of the line
					     means: a date on somebody else's task is their deadline, not
					     yours. -->
					<span class="assignee" title={t('task.assignee', { name: assignee.name })}>
						<span class="face" style="background: {assignee.avatar_color}">
							{assignee.name[0]?.toUpperCase() ?? '?'}
						</span>
						{assignee.name}
					</span>
				{/if}
				{#if snoozed}
					<span class="snooze-mark" title={t('task.snoozedUntil', { when: snoozedLabel })}>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M4 6h8l-8 8h8" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" />
						</svg>
						{snoozedLabel}
					</span>
				{/if}
				{#if due}
					<span class="due" data-tone={due.tone}>
						{due.text}{#if time}&nbsp;{time}{/if}
					</span>
				{/if}
				{#if repeats}
					<span class="repeat" title={t('task.repeats', { rule: repeats })}>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M17 2l4 4-4 4" />
							<path d="M3 11V9a4 4 0 014-4h14" />
							<path d="M7 22l-4-4 4-4" />
							<path d="M21 13v2a4 4 0 01-4 4H3" />
						</svg>
						{repeats}
					</span>
				{/if}
				<!-- What is underneath, said as a fraction rather than a total: "3" tells
				     you there is something there, "1/3" tells you whether there is
				     anything left to do, which is the question the list is for. -->
				{#if task.subtask_count}
					<span
						class="badge"
						class:all-done={task.subtask_done === task.subtask_count}
						title={t('task.subtasks', { done: task.subtask_done, total: task.subtask_count })}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M9 6h11" />
							<path d="M9 12h11" />
							<path d="M9 18h11" />
							<path d="M4 6l1 1 2-2" />
							<path d="M4 12l1 1 2-2" />
							<path d="M4 18l1 1 2-2" />
						</svg>
						{task.subtask_done}/{task.subtask_count}
					</span>
				{/if}
				{#if task.attachment_count}
					<span class="badge" title={t('task.attachments', { n: task.attachment_count })}>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path
								d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48"
							/>
						</svg>
						{task.attachment_count}
					</span>
				{/if}
				{#each task.labels ?? [] as label}
					<span class="label">{label}</span>
				{/each}
			</span>
		{/if}
	</button>
</div>

<style>
	.row {
		display: flex;
		gap: var(--s3);
		align-items: flex-start;
		padding: var(--s3) var(--s2);
		border-bottom: 1px solid var(--line);
		transition:
			opacity var(--medium) var(--ease),
			transform var(--medium) var(--ease);
	}

	.row:hover {
		background: var(--surface);
	}

	/* The exit: a short lift and fade, so completing something reads as the task
	   leaving rather than as the list glitching. */
	.row.leaving {
		opacity: 0;
		transform: translateX(6px);
	}

	.row.completed .content {
		color: var(--ink-faint);
		text-decoration: line-through;
		text-decoration-color: var(--ink-faint);
	}

	/* Parked, not gone: dimmed so it reads as set aside, but still there and still
	   readable. The checkbox and the snooze mark keep their weight, so it can be
	   woken or ticked off without peering at it. */
	.row.snoozed .content,
	.row.snoozed .description,
	.row.snoozed .meta {
		opacity: 0.5;
	}
	.snooze-mark {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		color: var(--ink-muted);
		opacity: 1;
	}
	.snooze-mark svg {
		width: 12px;
		height: 12px;
	}

	.check {
		flex: none;
		width: 20px;
		height: 20px;
		margin-top: 2px;
		border: 1.5px solid var(--line-strong);
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		transition:
			border-color var(--fast) var(--ease),
			background var(--fast) var(--ease),
			transform var(--fast) var(--ease);
	}

	/* Priority is carried by the checkbox ring rather than by a separate badge:
	   it is the thing your eye is already going to, and one signal beats two. */
	.row[data-priority='1'] .check {
		border-color: var(--p1);
		background: color-mix(in srgb, var(--p1) 12%, transparent);
	}
	.row[data-priority='2'] .check {
		border-color: var(--p2);
		background: color-mix(in srgb, var(--p2) 12%, transparent);
	}
	.row[data-priority='3'] .check {
		border-color: var(--p3);
		background: color-mix(in srgb, var(--p3) 12%, transparent);
	}

	.check:hover {
		border-color: var(--accent);
		transform: scale(1.08);
	}

	.check.checked {
		background: var(--accent);
		border-color: var(--accent);
	}

	.check svg {
		width: 14px;
		height: 14px;
		fill: none;
		stroke: var(--accent-ink);
		stroke-width: 2.5;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* The checkmark draws itself rather than appearing. 18 is a little over the
	   path length, which keeps it fully hidden before the animation starts. */
	.tick {
		stroke-dasharray: 18;
		stroke-dashoffset: 18;
		transition: stroke-dashoffset var(--medium) var(--ease-out);
	}

	.check.checked .tick {
		stroke-dashoffset: 0;
	}

	.body {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 2px;
		text-align: left;
		min-width: 0;
	}

	.content {
		color: var(--ink);
		line-height: 1.45;
		overflow-wrap: anywhere;
	}

	.description {
		font-size: var(--text-sm);
		color: var(--ink-muted);
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: var(--s2);
		align-items: center;
		margin-top: var(--s1);
		font-size: var(--text-xs);
	}

	.due[data-tone='overdue'] {
		color: var(--p1);
	}
	.due[data-tone='today'] {
		color: var(--accent);
	}
	.due[data-tone='soon'],
	.due[data-tone='later'] {
		color: var(--ink-muted);
	}

	.repeat {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		color: var(--ink-muted);
	}

	.repeat svg {
		width: 11px;
		height: 11px;
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.assignee {
		display: inline-flex;
		align-items: center;
		gap: var(--s1);
		color: var(--ink-muted);
	}

	.face {
		width: 14px;
		height: 14px;
		border-radius: var(--radius-full);
		display: inline-grid;
		place-items: center;
		font-size: 9px;
		font-weight: 560;
		color: #fff;
		flex: none;
	}

	/* A mark, not a chip: it sits in the same row as the date and the labels and
	   must not out-shout them. The task's own text is the thing being read. */
	.badge {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		color: var(--ink-faint);
		flex: none;
	}

	.badge svg {
		width: 12px;
		height: 12px;
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* Everything underneath is closed. Worth saying, because "3/3" and "1/3" are
	   the two answers you actually look for and only one of them is good news. */
	.badge.all-done {
		color: var(--accent);
	}

	.label {
		color: var(--ink-muted);
		background: var(--surface-raised);
		border: 1px solid var(--line);
		padding: 1px var(--s2);
		border-radius: var(--radius-full);
	}
</style>
