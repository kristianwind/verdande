/**
 * The one place the app talks to the server.
 *
 * Every call goes through `request`, so authentication failures, error shapes and
 * the CSRF header are handled once rather than at each of forty call sites.
 */

/** Thrown for any non-2xx response. Carries the server's stable `code` so callers
 *  can branch on the reason without matching on prose. */
export class ApiError extends Error {
	constructor(status, code, message, fields) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
		this.code = code;
		this.fields = fields ?? {};
	}

	/** True when the session has gone — the app should show the sign-in screen. */
	get isAuthError() {
		return this.status === 401;
	}
}

import { t } from './i18n.svelte.js';

/**
 * The key for each code the API can return.
 *
 * Keys rather than sentences, because the server's own prose is English and
 * written for a log — what a person reads is decided here, where the locale is
 * known. `errorcodes_test.go` walks this table against the codes Go can emit, so
 * a code the server gained and this did not fails the build rather than falling
 * through to that log prose.
 */
const MESSAGES = {
	unauthorized: 'error.unauthorized',
	totp_required: 'error.totpRequired',
	forbidden: 'error.forbidden',
	not_found: 'error.notFound',
	conflict: 'error.conflict',
	rate_limited: 'error.rateLimited',
	validation_failed: 'error.validation',
	payload_too_large: 'error.tooLarge',
	internal_error: 'error.internal',
	bad_request: 'error.badRequest',
	// Client-only: a network failure is not an HTTP status, so the server has no
	// code for it.
	offline: 'error.offline',

	// Situations that used to arrive as a plain `conflict` and were therefore
	// shown as "that already exists" — which was not merely vague but wrong. An
	// unconfigured Gmail is not a Gmail that is already connected.
	gmail_not_configured: 'error.gmailNotConfigured',
	ai_not_configured: 'error.aiNotConfigured',
	totp_not_enabled: 'error.totpNotEnabled',
	inbox_protected: 'error.inboxProtected',
	last_admin: 'error.lastAdmin'
};

/**
 * What to show a person for a failed request.
 *
 * A code with no key here falls back to the server's prose, which is English and
 * written for a log — not good, but better than swallowing the only explanation
 * there is. A code that turns up in that fallback is a code this table is missing.
 */
export function humanMessage(err) {
	if (!(err instanceof ApiError)) return t(MESSAGES.offline);
	const key = MESSAGES[err.code];
	if (key) return t(key);
	return err.message ?? t(MESSAGES.internal_error);
}

async function request(method, path, body, options = {}) {
	const init = {
		method,
		headers: {},
		// The session cookie is httpOnly and same-origin; nothing here handles a token.
		credentials: 'same-origin',
		signal: options.signal
	};
	if (body !== undefined) {
		init.headers['Content-Type'] = 'application/json';
		init.body = JSON.stringify(body);
	}

	let res;
	try {
		res = await fetch(`/api/v1${path}`, init);
	} catch (e) {
		// A network failure is not an HTTP status; give it one the app can handle
		// the same way as everything else.
		throw new ApiError(0, 'offline', 'ingen forbindelse');
	}

	if (res.status === 204) return null;

	let payload = null;
	const text = await res.text();
	if (text) {
		try {
			payload = JSON.parse(text);
		} catch {
			payload = null;
		}
	}

	if (!res.ok) {
		throw new ApiError(
			res.status,
			payload?.code ?? 'internal_error',
			payload?.error ?? res.statusText,
			payload?.fields
		);
	}
	return payload;
}

const get = (path, options) => request('GET', path, undefined, options);
const post = (path, body, options) => request('POST', path, body ?? {}, options);
const patch = (path, body) => request('PATCH', path, body);
const put = (path, body) => request('PUT', path, body);
const del = (path) => request('DELETE', path);

/**
 * Uploads a file. Not routed through `request`, which serialises JSON and sets a
 * Content-Type — a multipart body has to set its own boundary, and the browser
 * only does that when nothing has claimed the header.
 */
async function upload(path, file, field = 'file') {
	const form = new FormData();
	form.append(field, file);

	let res;
	try {
		res = await fetch(`/api/v1${path}`, { method: 'POST', body: form, credentials: 'same-origin' });
	} catch {
		throw new ApiError(0, 'offline', 'ingen forbindelse');
	}

	const text = await res.text();
	let payload = null;
	if (text) {
		try {
			payload = JSON.parse(text);
		} catch {
			payload = null;
		}
	}
	if (!res.ok) {
		throw new ApiError(
			res.status,
			payload?.code ?? 'internal_error',
			payload?.error ?? res.statusText,
			payload?.fields
		);
	}
	return payload;
}

export const api = {
	// --- auth
	setupState: () => get('/auth/setup'),
	setup: (data) => post('/auth/setup', data),
	login: (email, password) => post('/auth/login', { email, password }),
	loginTOTP: (code) => post('/auth/login/totp', { code }),
	logout: () => post('/auth/logout'),
	me: () => get('/auth/me'),
	updateProfile: (data) => patch('/auth/me', data),
	// Its own route rather than a field on the profile: that one backs a form with
	// a save button, and this writes on every click of a chevron.
	setSidebarSections: (sections) => put('/auth/sidebar-sections', { sections }),
	signup: (data) => post('/auth/signup', data),
	forgotPassword: (email) => post('/auth/password/forgot', { email }),
	resetPassword: (token, password) => post('/auth/password/reset', { token, password }),
	changePassword: (current_password, new_password) =>
		post('/auth/password/change', { current_password, new_password }),

	totpSetup: () => post('/auth/totp/setup'),
	totpConfirm: (code) => post('/auth/totp/confirm', { code }),
	totpDisable: (password) => post('/auth/totp/disable', { password }),
	listSessions: () => get('/auth/sessions'),
	endSession: (id) => del(`/auth/sessions/${id}`),

	// --- users, administrators only
	listUsers: () => get('/users'),
	inviteUser: (email) => post('/users', { email }),
	setUserAdmin: (id, isAdmin) => patch(`/users/${id}`, { is_admin: isAdmin }),
	deleteUser: (id) => del(`/users/${id}`),
	revokeInvite: (id) => del(`/invites/${id}`),
	listErrors: () => get('/errors'),

	// The nightly backup, which ran since the beginning with nothing to show it.
	// The OAuth client itself, which is the instance's registration with Google
	// rather than anybody's mailbox.
	gmailClient: () => get('/gmail/client'),
	setGmailClient: (data) => put('/gmail/client', data),

	listBackups: () => get('/backups'),
	runBackup: () => post('/backups'),
	// A link rather than a fetch: the browser's own download handles a file of
	// this size, and a blob in memory of the whole database does not.
	backupURL: (id) => `/api/v1/backups/${id}`,

	// The instance-wide history, which is the other half of the error log: that
	// one is what broke, this is what was done. Paged with the cursor the server
	// hands back rather than an offset — see the OpenAPI note on /activity.
	auditLog: ({ before, user_id, project_id, event, limit } = {}) => {
		const q = new URLSearchParams();
		if (before) q.set('before', before);
		if (user_id) q.set('user_id', user_id);
		if (project_id) q.set('project_id', project_id);
		if (event) q.set('event', event);
		if (limit) q.set('limit', limit);
		const query = q.toString();
		return get(`/activity${query ? `?${query}` : ''}`);
	},
	auditEvents: () => get('/activity/events'),

	recoveryCodes: () => get('/auth/recovery-codes'),
	regenerateRecoveryCodes: (password) => post('/auth/recovery-codes', { password }),

	// --- projects
	listProjects: (archived = false) => get(`/projects${archived ? '?archived=true' : ''}`),
	getProject: (id) => get(`/projects/${id}`),
	createProject: (data) => post('/projects', data),
	updateProject: (id, data) => patch(`/projects/${id}`, data),
	deleteProject: (id) => del(`/projects/${id}`),
	reorderProjects: (ids) => post('/projects/reorder', { ids }),
	listTrashedProjects: () => get('/trash/projects'),
	restoreProject: (id) => post(`/trash/projects/${id}/restore`),

	listProjectGroups: () => get('/project-groups'),
	createProjectGroup: (name) => post('/project-groups', { name }),
	getProjectGroup: (id) => get(`/project-groups/${id}`),
	updateProjectGroup: (id, data) => patch(`/project-groups/${id}`, data),
	uploadGroupAttachment: (id, file) => upload(`/project-groups/${id}/attachments`, file),
	deleteProjectGroup: (id) => del(`/project-groups/${id}`),
	reorderProjectGroups: (ids) => post('/project-groups/reorder', { ids }),

	listSections: (projectId) => get(`/projects/${projectId}/sections`),
	createSection: (projectId, name) => post(`/projects/${projectId}/sections`, { name }),
	updateSection: (id, data) => patch(`/sections/${id}`, data),
	deleteSection: (id) => del(`/sections/${id}`),

	listMembers: (projectId) => get(`/projects/${projectId}/members`),
	invite: (projectId, email, role) => post(`/projects/${projectId}/invites`, { email, role }),
	setMemberRole: (projectId, userId, role) =>
		patch(`/projects/${projectId}/members/${userId}`, { role }),
	removeMember: (projectId, userId) => del(`/projects/${projectId}/members/${userId}`),
	activity: (projectId) => get(`/projects/${projectId}/activity`),

	// --- tasks
	listTasks: (params = {}) => {
		const query = new URLSearchParams(
			Object.entries(params).filter(([, v]) => v !== undefined && v !== '')
		);
		const suffix = query.toString();
		return get(`/tasks${suffix ? `?${suffix}` : ''}`);
	},
	getTask: (id) => get(`/tasks/${id}`),
	createTask: (data) => post('/tasks', data),
	updateTask: (id, data) => patch(`/tasks/${id}`, data),
	deleteTask: (id) => del(`/tasks/${id}`),
	completeTask: (id) => post(`/tasks/${id}/complete`),
	reopenTask: (id) => post(`/tasks/${id}/reopen`),
	moveTask: (id, data) => post(`/tasks/${id}/move`, data),

	quickAdd: (text, projectId) => post('/tasks/quick-add', { text, project_id: projectId }),
	quickAddPreview: (text, signal) =>
		get(`/tasks/quick-add/preview?text=${encodeURIComponent(text)}`, { signal }),

	// --- filters and labels
	listFilters: () => get('/filters'),
	createFilter: (data) => post('/filters', data),
	updateFilter: (id, data) => patch(`/filters/${id}`, data),
	deleteFilter: (id) => del(`/filters/${id}`),
	runFilter: (id) => get(`/filters/${id}/tasks`),
	previewFilter: (query) => get(`/filters/preview?query=${encodeURIComponent(query)}`),

	listLabels: () => get('/labels'),
	createLabel: (name) => post('/labels', { name }),
	deleteLabel: (id) => del(`/labels/${id}`),

	// --- reminders, feed and templates
	listReminders: (taskId) => get(`/tasks/${taskId}/reminders`),
	createReminder: (taskId, data) => post(`/tasks/${taskId}/reminders`, data),
	deleteReminder: (id) => del(`/reminders/${id}`),

	feed: () => get('/feed'),
	rotateFeed: () => post('/feed/rotate'),

	listTemplates: () => get('/templates'),
	saveTemplate: (data) => post('/templates', data),
	useTemplate: (id, data) => post(`/templates/${id}/use`, data),
	deleteTemplate: (id) => del(`/templates/${id}`),

	// --- comments and attachments
	listComments: (taskId) => get(`/tasks/${taskId}/comments`),
	createComment: (taskId, body) => post(`/tasks/${taskId}/comments`, { body }),
	updateComment: (id, body) => patch(`/comments/${id}`, { body }),
	deleteComment: (id) => del(`/comments/${id}`),

	uploadAttachment: (taskId, file) => upload(`/tasks/${taskId}/attachments`, file),
	deleteAttachment: (id) => del(`/attachments/${id}`),
	attachmentURL: (id) => `/api/v1/attachments/${id}`,

	// --- API tokens
	listTokens: () => get('/tokens'),
	createToken: (name, expiresInDays) =>
		post('/tokens', { name, expires_in_days: expiresInDays ?? 0 }),
	deleteToken: (id) => del(`/tokens/${id}`),

	// --- notifications and push
	listNotifications: () => get('/notifications'),
	markNotificationsRead: (id) => post(id ? `/notifications/${id}/read` : '/notifications/read'),

	pushKey: () => get('/push/key'),
	subscribePush: (subscription) => post('/push/subscribe', subscription),
	unsubscribePush: (endpoint) => post('/push/unsubscribe', { endpoint, keys: {} }),

	// --- integrations
	getMailAddress: () => get('/mail-address'),
	rotateMailAddress: () => post('/mail-address/rotate'),

	getGmail: () => get('/gmail'),
	setGmail: (data) => put('/gmail', data),
	disconnectGmail: () => del('/gmail'),
	authorizeGmail: () => post('/gmail/authorize'),
	syncGmail: () => post('/gmail/sync'),

	getAISettings: () => get('/ai/settings'),
	setAISettings: (data) => put('/ai/settings', data),
	aiSummary: () => post('/ai/summary'),
	aiSplit: (taskId) => post(`/ai/tasks/${taskId}/split`),

	version: () => get('/version'),

	// --- import and export
	importTodoist: (file) => upload('/import/todoist', file),
	importCSV: (data) => post('/import/csv', data),
	exportAccountURL: () => '/api/v1/export/account',
	exportProjectCSVURL: (id) => `/api/v1/export/projects/${id}.csv`,
	exportProjectICSURL: (id) => `/api/v1/export/projects/${id}.ics`,

	// --- views
	today: () => get('/today'),
	upcoming: (days) => get(`/upcoming${days ? `?days=${days}` : ''}`),
	delegated: () => get('/delegated'),
	people: () => get('/people'),
	search: (q) => get(`/search?q=${encodeURIComponent(q)}`)
};
