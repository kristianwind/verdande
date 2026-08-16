/**
 * Application state.
 *
 * Svelte 5 runes rather than stores: the task list is read by half the components
 * in the app, and `$state` gives fine-grained reactivity without every one of them
 * subscribing and unsubscribing by hand.
 *
 * The rule this file exists to enforce: **every local action is applied
 * immediately and reconciled afterwards**. Ticking a checkbox must not wait for a
 * round trip. When the server disagrees, the change is rolled back and the person
 * is told — which is rare, and much better than a spinner on every click.
 */

import { api, ApiError, humanMessage } from './api.js';

class AppState {
	user = $state(null);
	projects = $state([]);
	tasks = $state([]);
	loading = $state(true);
	/** Transient messages: a failed save, a rolled-back change. */
	toasts = $state([]);
	connected = $state(false);

	#socket = null;
	#reconnectDelay = 1000;

	get inbox() {
		return this.projects.find((p) => p.is_inbox);
	}

	projectById(id) {
		return this.projects.find((p) => p.id === id);
	}

	async load() {
		this.loading = true;
		try {
			this.user = await api.me();
			const { projects } = await api.listProjects();
			this.projects = projects;
			this.connect();
		} catch (e) {
			this.user = null;
		} finally {
			this.loading = false;
		}
	}

	// --- realtime ---------------------------------------------------------------

	connect() {
		if (this.#socket) return;
		const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
		const socket = new WebSocket(`${scheme}://${location.host}/api/v1/ws`);
		this.#socket = socket;

		socket.onopen = () => {
			this.connected = true;
			this.#reconnectDelay = 1000;
		};
		socket.onmessage = (event) => {
			try {
				this.#applyRemote(JSON.parse(event.data));
			} catch {
				// A message this build does not understand is not worth a crash.
			}
		};
		socket.onclose = () => {
			this.connected = false;
			this.#socket = null;
			// Backing off matters: a server that is restarting should not be met
			// with a reconnect every 50ms from every open tab.
			setTimeout(() => this.connect(), this.#reconnectDelay);
			this.#reconnectDelay = Math.min(this.#reconnectDelay * 2, 30000);
		};
		socket.onerror = () => socket.close();
	}

	/** Applies a change made by somebody else. */
	#applyRemote(event) {
		const task = event.payload;
		switch (event.type) {
			case 'task.created':
			case 'task.updated':
			case 'task.completed':
			case 'task.reopened':
			case 'task.moved': {
				if (!task?.id) return;
				const i = this.tasks.findIndex((t) => t.id === task.id);
				if (i >= 0) this.tasks[i] = task;
				else this.tasks.push(task);
				break;
			}
			case 'task.deleted':
				this.tasks = this.tasks.filter((t) => t.id !== task?.id);
				break;
		}
	}

	// --- tasks ------------------------------------------------------------------

	async loadTasks(params) {
		const { tasks } = await api.listTasks(params);
		this.tasks = tasks;
		return tasks;
	}

	/**
	 * Ticks a task off. The row is struck through and gone before the request
	 * leaves — which is the single most-repeated action in the app, and the one
	 * place latency would be felt all day.
	 */
	async complete(id) {
		const index = this.tasks.findIndex((t) => t.id === id);
		if (index < 0) return;
		const previous = this.tasks[index];

		this.tasks[index] = { ...previous, completed: true };
		try {
			const updated = await api.completeTask(id);
			const i = this.tasks.findIndex((t) => t.id === id);
			if (i >= 0) this.tasks[i] = updated;
		} catch (e) {
			const i = this.tasks.findIndex((t) => t.id === id);
			if (i >= 0) this.tasks[i] = previous;
			this.toast(humanMessage(e));
		}
	}

	async reopen(id) {
		const index = this.tasks.findIndex((t) => t.id === id);
		if (index < 0) return;
		const previous = this.tasks[index];

		this.tasks[index] = { ...previous, completed: false };
		try {
			const updated = await api.reopenTask(id);
			const i = this.tasks.findIndex((t) => t.id === id);
			if (i >= 0) this.tasks[i] = updated;
		} catch (e) {
			const i = this.tasks.findIndex((t) => t.id === id);
			if (i >= 0) this.tasks[i] = previous;
			this.toast(humanMessage(e));
		}
	}

	async update(id, patch) {
		const index = this.tasks.findIndex((t) => t.id === id);
		if (index < 0) return;
		const previous = this.tasks[index];

		this.tasks[index] = { ...previous, ...patch };
		try {
			const updated = await api.updateTask(id, patch);
			const i = this.tasks.findIndex((t) => t.id === id);
			if (i >= 0) this.tasks[i] = updated;
		} catch (e) {
			const i = this.tasks.findIndex((t) => t.id === id);
			if (i >= 0) this.tasks[i] = previous;
			this.toast(humanMessage(e));
		}
	}

	async remove(id) {
		const previous = [...this.tasks];
		this.tasks = this.tasks.filter((t) => t.id !== id);
		try {
			await api.deleteTask(id);
		} catch (e) {
			this.tasks = previous;
			this.toast(humanMessage(e));
		}
	}

	/**
	 * Adds a task from the quick-add box.
	 *
	 * This one is *not* fully optimistic: the parse that turns one line into a
	 * date, a project and a priority happens on the server, so inventing a row
	 * here would show a task that visibly rearranges itself a moment later. The
	 * request is fast and the box clears immediately, which is where the
	 * responsiveness actually needs to be.
	 */
	async quickAdd(text, projectId) {
		try {
			const task = await api.quickAdd(text, projectId);
			this.tasks.push(task);
			return task;
		} catch (e) {
			this.toast(humanMessage(e));
			return null;
		}
	}

	// --- projects ---------------------------------------------------------------

	async createProject(name) {
		try {
			const project = await api.createProject({ name });
			this.projects.push(project);
			return project;
		} catch (e) {
			this.toast(humanMessage(e));
			return null;
		}
	}

	async refreshProjects() {
		const { projects } = await api.listProjects();
		this.projects = projects;
	}

	// --- toasts -------------------------------------------------------------------

	toast(message) {
		const id = Math.random().toString(36).slice(2);
		this.toasts.push({ id, message });
		setTimeout(() => {
			this.toasts = this.toasts.filter((t) => t.id !== id);
		}, 5000);
	}

	dismissToast(id) {
		this.toasts = this.toasts.filter((t) => t.id !== id);
	}
}

export const app = new AppState();

/** Theme, kept in localStorage and applied to the document element. */
export const theme = {
	get current() {
		if (typeof document === 'undefined') return 'dark';
		return document.documentElement.dataset.theme ?? 'dark';
	},
	toggle() {
		const next = this.current === 'dark' ? 'light' : 'dark';
		document.documentElement.dataset.theme = next;
		try {
			localStorage.setItem('verdande:theme', next);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
};

export { ApiError, humanMessage };
