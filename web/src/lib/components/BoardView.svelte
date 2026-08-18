<script>
	/**
	 * Kanban: one column per section, plus one for everything not filed under a
	 * section. Dragging a card between columns moves the task.
	 *
	 * The drag uses the native HTML drag-and-drop API rather than pointer events.
	 * It is fiddlier to style, but it is the one that works with a trackpad, a
	 * mouse and a screen reader's drag affordance without reimplementing any of
	 * them — and it gives keyboard users the browser's own behaviour for free.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { api, humanMessage } from '$lib/api.js';
	import { TASK, startDrag, carries, accept } from '$lib/dnd.js';
	import { focusOnMount } from '$lib/focus.js';
	import TaskRow from './TaskRow.svelte';
	import { t } from '$lib/i18n.svelte.js';

	let { project, sections, canEdit, onsectionadded } = $props();

	// --- adding a column ------------------------------------------------------------
	//
	// A column on a board *is* a section, so this is where somebody looks for one.
	// It was only in the list view, which meant a board — the view whose entire
	// shape is its sections — had no way to grow one. Reported as "sections have no
	// function: there is no way to create them", which from inside a board is
	// exactly true.

	let adding = $state(false);
	let name = $state('');

	/** Same as the list view's: through quick add, so a date or a p1 still parses. */
	let addingIn = $state(null);
	let newInSection = $state('');

	async function addToSection(event, sectionId) {
		event.preventDefault();
		const text = newInSection.trim();
		if (!text) return;
		newInSection = '';
		addingIn = null;
		try {
			app.upsert(await api.quickAdd(text, project.id, sectionId));
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function addSection(event) {
		event.preventDefault();
		const trimmed = name.trim();
		if (!trimmed) return;
		adding = false;
		name = '';
		try {
			onsectionadded?.(await api.createSection(project.id, trimmed));
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	let draggingId = $state(null);
	let overColumn = $state(null);

	// The unsectioned column comes first, as it does in the list view: it is where
	// quick add puts things, so it is where new work appears.
	let columns = $derived([
		{ id: '', name: 'Uden sektion' },
		...sections.map((s) => ({ id: s.id, name: s.name }))
	]);

	// Scoped to this project, not just to the section: the store holds whatever the
	// last view loaded, and a task that has left the project would otherwise turn
	// up in the "no section" column, which is exactly where a task with no section
	// belongs — in some other project.
	const tasksIn = (sectionId) =>
		app.tasks
			.filter(
				(t) =>
					!t.completed &&
					!t.parent_id &&
					t.project_id === project.id &&
					(t.section_id ?? '') === sectionId
			)
			.sort((a, b) => a.sort_order - b.sort_order);

	function onDragStart(event, task) {
		if (!canEdit) return;
		draggingId = task.id;
		startDrag(event, TASK, task.id);
	}

	function onDragOver(event, sectionId) {
		if (!canEdit || !carries(event, TASK) || !draggingId) return;
		accept(event);
		overColumn = sectionId;
	}

	async function onDrop(event, sectionId) {
		event.preventDefault();
		overColumn = null;
		const id = draggingId;
		draggingId = null;
		if (!id || !canEdit) return;

		const task = app.get(id);
		if (!task || (task.section_id ?? '') === sectionId) return;

		// Optimistic: the card is in the new column before the request goes out,
		// because a card that snaps back for 200ms reads as a failed drop.
		const previous = { ...task };
		app.replace(id, { ...task, section_id: sectionId });

		try {
			// Dropped at the end of the column: the task after it is nothing, and
			// the one before it is whatever was last there.
			const existing = tasksIn(sectionId).filter((t) => t.id !== id);
			const after = existing.at(-1);
			app.replace(
				id,
				await api.moveTask(id, {
					project_id: project.id,
					section_id: sectionId,
					after_id: after?.id ?? ''
				})
			);
		} catch {
			app.replace(id, previous);
			app.toast('Kunne ikke flytte opgaven.');
		}
	}
</script>

<div class="board">
	{#each columns as column (column.id)}
		{@const tasks = tasksIn(column.id)}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<section
			class="column"
			class:over={overColumn === column.id}
			ondragover={(e) => onDragOver(e, column.id)}
			ondragleave={() => (overColumn = null)}
			ondrop={(e) => onDrop(e, column.id)}
		>
			<header>
				<h2>{column.name}</h2>
				<span class="count">{tasks.length}</span>
			</header>

			<div class="cards">
				{#each tasks as task (task.id)}
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<div
						class="card"
						class:dragging={draggingId === task.id}
						draggable={canEdit}
						ondragstart={(e) => onDragStart(e, task)}
						ondragend={() => (draggingId = null)}
					>
						<TaskRow {task} />
					</div>
				{/each}

				{#if !tasks.length}
					<p class="empty">{t('view.emptySection')}</p>
				{/if}
			</div>

			<!-- At the foot of the column, where the next card would go. A board's
			     columns *are* the sections, so this is the one place where "add a
			     task here" needs no explanation at all. -->
			{#if canEdit}
				<!-- `column`, which is what the each binds. The unsectioned column has
				     an empty id, and that is exactly what quick add wants for "in this
				     project, no section". -->
				{#if addingIn === column.id}
					<form class="add-task" onsubmit={(e) => addToSection(e, column.id)}>
						<input
							bind:value={newInSection}
							use:focusOnMount
							placeholder={t('view.addTaskHere')}
							aria-label={t('view.addTaskIn', { name: column.name })}
							onblur={() => !newInSection.trim() && (addingIn = null)}
							onkeydown={(e) => e.key === 'Escape' && (addingIn = null)}
						/>
					</form>
				{:else}
					<button
						class="add-task-open"
						onclick={() => {
							addingIn = column.id;
							newInSection = '';
						}}>{t('view.addTask')}</button
					>
				{/if}
			{/if}
		</section>
	{/each}

	{#if canEdit}
		<section class="column adder">
			{#if adding}
				<form onsubmit={addSection}>
					<input
						bind:value={name}
						use:focusOnMount
						placeholder={t('view.sectionName')}
						aria-label={t('view.newSection')}
						onblur={() => !name.trim() && (adding = false)}
						onkeydown={(e) => e.key === 'Escape' && (adding = false)}
					/>
				</form>
			{:else}
				<button
					class="add"
					onclick={() => {
						adding = true;
						name = '';
					}}>{t('view.addSection')}</button
				>
			{/if}
		</section>
	{/if}
</div>

<style>
	.board {
		display: flex;
		gap: var(--s3);
		align-items: flex-start;
		overflow-x: auto;
		padding-bottom: var(--s4);
		/* Columns snap as you swipe on a phone, so a half-column never sits at
		   the edge looking like a rendering fault. */
		scroll-snap-type: x proximity;
	}

	/* Narrower than a real column and without its background: it is an invitation
	   at the end of the row, not a column with nothing in it. */
	.adder {
		background: none;
		border: 1px dashed var(--line);
		min-width: 180px;
		padding: var(--s2);
	}

	.adder .add {
		width: 100%;
		padding: var(--s2);
		font-size: var(--text-sm);
		color: var(--ink-faint);
		border-radius: var(--radius);
		text-align: left;
	}

	.adder .add:hover {
		color: var(--ink);
		background: var(--surface);
	}

	.adder input {
		width: 100%;
		padding: var(--s2);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
	}

	.adder input:focus {
		border-color: var(--accent);
	}

	.column {
		flex: 0 0 300px;
		max-width: 300px;
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius-lg);
		padding: var(--s3);
		scroll-snap-align: start;
		transition: border-color var(--fast) var(--ease);
	}

	/* The drop target is shown by the column it would land in, not by a line
	   between cards: the column is the decision being made here. */
	.column.over {
		border-color: var(--accent);
		background: var(--accent-sunken);
	}

	header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--s2);
		padding: 0 var(--s2) var(--s3);
	}

	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.count {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		flex: none;
	}

	.cards {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		min-height: 60px;
	}

	.card {
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		padding: 0 var(--s2);
		cursor: grab;
		transition:
			opacity var(--fast) var(--ease),
			border-color var(--fast) var(--ease);
	}

	.card:hover {
		border-color: var(--line-strong);
	}

	.card.dragging {
		opacity: 0.4;
		cursor: grabbing;
	}

	/* The row draws its own bottom border for the list view, which is a stray
	   line inside a card. */
	.card :global(.row) {
		border-bottom: none;
	}

	.card :global(.row:hover) {
		background: transparent;
	}

	.add-task-open {
		align-self: flex-start;
		padding: var(--s2);
		font-size: var(--text-sm);
		color: var(--ink-faint);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	.column:hover .add-task-open,
	.add-task-open:focus-visible {
		opacity: 1;
	}

	.add-task-open:hover {
		color: var(--ink-muted);
	}

	.add-task input {
		width: 100%;
		padding: var(--s2);
		background: var(--ground);
		border: 1px solid var(--accent);
		border-radius: var(--radius);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--ink);
		outline: none;
	}

	.empty {
		margin: 0;
		padding: var(--s3) var(--s2);
		color: var(--ink-faint);
		font-size: var(--text-sm);
		text-align: center;
	}
</style>
