/**
 * What each recorded activity event is called.
 *
 * The keys are the server's and they are stable; this is the only place they get a
 * name. It lives in its own module because two surfaces read the same log now — a
 * project's own history panel, and the administrator's instance-wide audit page —
 * and two copies of twenty strings is two places for them to drift apart.
 *
 * The names themselves are in the dictionaries rather than here. They were written
 * out in Danish, which made the whole of both history pages Danish for somebody
 * reading the rest of the app in English.
 *
 * An unknown key falls through to the key itself rather than being hidden: a build
 * that records something this table has not learned yet should still show that it
 * happened.
 */
import { t } from './i18n.svelte.js';

export const EVENTS = {
	'project.created': 'event.projectCreated',
	'project.updated': 'event.projectUpdated',
	'project.deleted': 'event.projectDeleted',
	'project.imported': 'event.projectImported',
	'section.created': 'event.sectionCreated',
	'section.updated': 'event.sectionUpdated',
	'section.deleted': 'event.sectionDeleted',
	'member.invited': 'event.memberInvited',
	'member.added': 'event.memberAdded',
	'member.removed': 'event.memberRemoved',
	'member.role_changed': 'event.memberRoleChanged',
	'task.created': 'event.taskCreated',
	'task.updated': 'event.taskUpdated',
	'task.completed': 'event.taskCompleted',
	'task.reopened': 'event.taskReopened',
	'task.moved': 'event.taskMoved',
	'task.deleted': 'event.taskDeleted',
	'task.split': 'event.taskSplit',
	'comment.created': 'event.commentCreated',
	'attachment.added': 'event.attachmentAdded'
};

/** The name of an event, or the raw key when it is one this build does not know. */
export function eventName(event) {
	const key = EVENTS[event];
	return key ? t(key) : event;
}

/** The bit of context an entry carries, when it has one worth reading. */
export function eventDetail(entry) {
	const p = entry.payload ?? {};
	return p.name ?? p.email ?? p.role ?? '';
}
