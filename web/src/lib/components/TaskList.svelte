<script>
	/**
	 * A list of tasks that can be reordered by dragging.
	 *
	 * The board view drags cards between columns; this drags rows within one. Both
	 * use the native HTML drag-and-drop API for the same reason — it works with a
	 * trackpad, a mouse and the browser's own affordances without reimplementing
	 * any of them.
	 *
	 * Where the board shows the target *column*, a list has to show the target
	 * *gap*: the whole decision here is "above this one or below it", so a line is
	 * drawn where the row would land.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { api } from '$lib/api.js';
	import TaskRow from './TaskRow.svelte';

	let { tasks, projectId, sectionId = '', canEdit = true } = $props();

	let draggingId = $state(null);
	/** The row the line is drawn at, and which side of it. */
	let overId = $state(null);
	let overBelow = $state(false);

	function onDragStart(event, task) {
		if (!canEdit) return;
		draggingId = task.id;
		event.dataTransfer.effectAllowed = 'move';
		// Firefox refuses to start a drag unless something is set on the transfer.
		event.dataTransfer.setData('text/plain', task.id);
	}

	function onDragOver(event, task) {
		if (!canEdit || !draggingId || draggingId === task.id) return;
		event.preventDefault();
		event.dataTransfer.dropEffect = 'move';

		// Which half of the row the pointer is in decides the gap. Comparing against
		// the row's own midpoint rather than counting rows is what keeps the line
		// where the eye expects it when rows have different heights.
		const box = event.currentTarget.getBoundingClientRect();
		overId = task.id;
		overBelow = event.clientY > box.top + box.height / 2;
	}

	function clearOver() {
		overId = null;
	}

	async function onDrop(event, target) {
		event.preventDefault();
		const id = draggingId;
		const below = overBelow;
		draggingId = null;
		overId = null;
		if (!id || !canEdit || id === target.id) return;

		const ordered = [...tasks].sort((a, b) => a.sort_order - b.sort_order);
		const without = ordered.filter((t) => t.id !== id);
		const at = without.findIndex((t) => t.id === target.id);
		if (at < 0) return;

		// The gap the row lands in, named by where they end up on screen — the API's
		// after_id is the task above, which is the opposite of what "after" reads as
		// in a vertical list.
		const index = below ? at + 1 : at;
		const above = without[index - 1];
		const beneath = without[index];

		const task = app.get(id);
		if (!task) return;
		const previous = { ...task };

		// Optimistic: the row is in its new place before the request goes out. The
		// midpoint is a local guess — the server owns the real value and sends it
		// back, and if the gap has run out of precision it respaces the section and
		// tries again.
		const order =
			above && beneath
				? (above.sort_order + beneath.sort_order) / 2
				: above
					? above.sort_order + 1024
					: (beneath?.sort_order ?? 1024) - 1024;

		app.replace(id, { ...task, sort_order: order });

		try {
			app.replace(
				id,
				await api.moveTask(id, {
					project_id: projectId,
					section_id: sectionId,
					after_id: above?.id ?? '',
					before_id: beneath?.id ?? ''
				})
			);
		} catch {
			app.replace(id, previous);
			app.toast('Kunne ikke flytte opgaven.');
		}
	}

	let ordered = $derived([...tasks].sort((a, b) => a.sort_order - b.sort_order));
</script>

{#each ordered as task (task.id)}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="sortable"
		class:dragging={draggingId === task.id}
		class:drop-above={overId === task.id && !overBelow}
		class:drop-below={overId === task.id && overBelow}
		draggable={canEdit}
		ondragstart={(e) => onDragStart(e, task)}
		ondragend={() => {
			draggingId = null;
			overId = null;
		}}
		ondragover={(e) => onDragOver(e, task)}
		ondragleave={clearOver}
		ondrop={(e) => onDrop(e, task)}
	>
		<TaskRow {task} />
	</div>
{/each}

<style>
	.sortable {
		position: relative;
		transition: opacity var(--fast) var(--ease);
	}

	/* Only the handle-less body drags; the checkbox stays clickable because the
	   drag needs a few pixels of movement before the browser calls it a drag. */
	.sortable[draggable='true'] {
		cursor: grab;
	}

	.sortable.dragging {
		opacity: 0.4;
		cursor: grabbing;
	}

	/* A line in the gap rather than a highlighted row: the row is not the target,
	   the space between two rows is. */
	.sortable::before {
		content: '';
		position: absolute;
		left: 0;
		right: 0;
		height: 2px;
		background: var(--accent);
		opacity: 0;
		pointer-events: none;
		transition: opacity var(--fast) var(--ease);
	}

	.sortable.drop-above::before {
		top: -1px;
		opacity: 1;
	}

	.sortable.drop-below::before {
		bottom: -1px;
		opacity: 1;
	}
</style>
