/**
 * Focuses an element the moment it is inserted.
 *
 *     <input use:focusOnMount />
 *
 * Replaces the `autofocus` attribute, which does not work in Safari for anything
 * added after the page has loaded. Every inline form in this app is added after
 * the page has loaded — the one that renames a project, the one that adds a
 * section, the one that names a group — so in Safari they all appeared with no
 * cursor in them. What you typed went nowhere.
 *
 * That is worse than it sounds, because it does not look like a bug. It looks like
 * the feature does not work: "sections have no function, because you cannot create
 * more than one" was this, and only this.
 *
 * The second attempt on the next frame is for the case where the element is
 * inserted inside something that is still settling — a drawer sliding in, a list
 * re-keying. Focusing an element the browser is still laying out can silently do
 * nothing, and there is no event to wait for that means "now".
 */
export function focusOnMount(node) {
	node.focus?.();
	if (document.activeElement !== node) {
		requestAnimationFrame(() => node.focus?.());
	}
}
