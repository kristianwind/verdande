/**
 * What a drag is carrying.
 *
 * More than one kind of thing is now dragged onto the same targets: the sidebar
 * takes a project (to reorder it, or file it under a group) *and* a task (to move
 * it into that project), and Kommende takes a task onto a day.
 *
 * The distinction has to be a MIME type rather than something read out of the
 * payload, because `getData` is deliberately unreadable while a drag is in the
 * air — the browser only hands the data over on drop, so that a page cannot read
 * what is being dragged across it. During `dragover`, `dataTransfer.types` is all
 * a drop target has to decide with, and deciding is exactly what it has to do:
 * `preventDefault` on a drag it cannot accept is what turns "no" into a silent,
 * wrong drop.
 */

export const TASK = 'application/x-verdande-task';
export const PROJECT = 'application/x-verdande-project';
export const GROUP = 'application/x-verdande-group';
export const NOTE = 'application/x-verdande-note';
export const SECTION = 'application/x-verdande-section';

/** Starts a drag carrying one id of the given kind. */
export function startDrag(event, type, id) {
	event.dataTransfer.effectAllowed = 'move';
	event.dataTransfer.setData(type, id);
	// Firefox refuses to start a drag unless text/plain is set as well.
	event.dataTransfer.setData('text/plain', id);
}

/** Whether the drag in flight is of this kind. Safe to call during dragover. */
export function carries(event, type) {
	return event.dataTransfer?.types?.includes(type) ?? false;
}

/** The id being dragged. Only meaningful in a drop handler. */
export function dragged(event, type) {
	return event.dataTransfer?.getData(type) ?? '';
}

/** Marks a drop target as willing, which is what allows the drop to happen at all. */
export function accept(event) {
	event.preventDefault();
	event.dataTransfer.dropEffect = 'move';
}
