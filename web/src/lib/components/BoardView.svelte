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
	import { api } from '$lib/api.js';
	import TaskRow from './TaskRow.svelte';

	let { project, sections, canEdit } = $props();

	let draggingId = $state(null);
	let overColumn = $state(null);

	// The unsectioned column comes first, as it does in the list view: it is where
	// quick add puts things, so it is where new work appears.
	let columns = $derived([
		{ id: '', name: 'Uden sektion' },
		...sections.map((s) => ({ id: s.id, name: s.name }))
	]);

	const tasksIn = (sectionId) =>
		app.tasks
			.filter((t) => !t.completed && !t.parent_id && (t.section_id ?? '') === sectionId)
			.sort((a, b) => a.sort_order - b.sort_order);

	function onDragStart(event, task) {
		if (!canEdit) return;
		draggingId = task.id;
		event.dataTransfer.effectAllowed = 'move';
		// Firefox refuses to start a drag unless something is set on the transfer.
		event.dataTransfer.setData('text/plain', task.id);
	}

	function onDragOver(event, sectionId) {
		if (!canEdit || !draggingId) return;
		event.preventDefault();
		event.dataTransfer.dropEffect = 'move';
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
					<p class="empty">Tom</p>
				{/if}
			</div>
		</section>
	{/each}
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

	.empty {
		margin: 0;
		padding: var(--s3) var(--s2);
		color: var(--ink-faint);
		font-size: var(--text-sm);
		text-align: center;
	}
</style>
