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
	import { t } from '$lib/i18n.svelte.js';
	import { api } from '$lib/api.js';
	import { TASK, startDrag, carries, dragged, accept } from '$lib/dnd.js';
	import TaskRow from './TaskRow.svelte';

	let { tasks, projectId, sectionId = '', canEdit = true } = $props();

	let draggingId = $state(null);
	/** The row the line is drawn at, and which side of it. */
	let overId = $state(null);
	let overBelow = $state(false);

	function onDragStart(event, task) {
		if (!canEdit) return;
		draggingId = task.id;
		// Typed, because the same task can now be dropped somewhere else entirely —
		// a project in the sidebar, a day in Kommende — and those targets have to be
		// able to tell what is coming while the drag is still in the air.
		startDrag(event, TASK, task.id);
	}

	function onDragOver(event, task) {
		// `carries` rather than `draggingId`: a task dragged from another list — the
		// unsectioned rows above, or another section — never called this component's
		// own dragstart, so its `draggingId` is null. Refusing on that made every
		// list reject everything that came from outside it, which is the whole point
		// of dragging between them.
		if (!canEdit || !carries(event, TASK) || draggingId === task.id) return;
		accept(event);

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
		// The section around this list also takes drops, for the coarse "into here"
		// gesture and for sections with no rows to aim at. A drop that landed on a
		// row has already said exactly where it goes, so it stops here.
		event.stopPropagation();
		// From the drag itself, not from `draggingId`, which is only set when the
		// drag *started* in this list. A task dragged in from the unsectioned rows
		// or another section left it null, so this returned early — and it had
		// already called stopPropagation, so the section around it never got the
		// drop either. The result: a task could only be dropped into a section with
		// no rows in it to aim at, which made every section look like it could hold
		// exactly one task.
		const id = dragged(event, TASK) || draggingId;
		const below = overBelow;
		draggingId = null;
		overId = null;
		if (!id || !canEdit || id === target.id) return;

		const ordered = [...tasks].sort((a, b) => a.sort_order - b.sort_order);
		// The dragged task may not be in this list at all — that is the cross-list
		// case — so `without` is "this list minus it" either way.
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
			app.toast(t('task.moveFailed'));
		}
	}

	// A snoozed task sinks below the rest, then rises back when its time passes —
	// the same order the server sorts a list in, kept here so a drag-sorted list
	// agrees rather than jumping when it reloads.
	const snoozedNow = (t) => t.snoozed_until && new Date(t.snoozed_until) > new Date();
	let ordered = $derived(
		[...tasks].sort((a, b) => {
			const sa = snoozedNow(a) ? 1 : 0;
			const sb = snoozedNow(b) ? 1 : 0;
			if (sa !== sb) return sa - sb;
			return a.sort_order - b.sort_order;
		})
	);
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
