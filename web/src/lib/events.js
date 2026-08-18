/**
 * What each recorded activity event is called.
 *
 * The keys are the server's and they are stable; this is the only place they
 * become Danish. It lives in its own module because two surfaces read the same log
 * now — a project's own history panel, and the administrator's instance-wide audit
 * page — and two copies of nineteen strings is two places for them to drift apart.
 *
 * An unknown key falls through to the key itself rather than being hidden: a build
 * that records something this table has not learned yet should still show that it
 * happened.
 */
export const EVENTS = {
	'project.created': 'oprettede projektet',
	'project.updated': 'ændrede projektet',
	'project.deleted': 'slettede projektet',
	'project.imported': 'importerede projektet',
	'section.created': 'oprettede en sektion',
	'section.updated': 'ændrede en sektion',
	'section.deleted': 'slettede en sektion',
	'member.invited': 'inviterede',
	'member.added': 'tilføjede',
	'member.removed': 'fjernede',
	'member.role_changed': 'ændrede rollen for',
	'task.created': 'oprettede en opgave',
	'task.updated': 'ændrede en opgave',
	'task.completed': 'lukkede en opgave',
	'task.reopened': 'genåbnede en opgave',
	'task.moved': 'flyttede en opgave',
	'task.deleted': 'slettede en opgave',
	'task.split': 'delte en opgave op',
	'comment.created': 'skrev en kommentar'
};

/** The name of an event, or the raw key when it is one this build does not know. */
export function eventName(event) {
	return EVENTS[event] ?? event;
}

/** The bit of context an entry carries, when it has one worth reading. */
export function eventDetail(entry) {
	const p = entry.payload ?? {};
	return p.name ?? p.email ?? p.role ?? '';
}
