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

	/**
	 * The task whose detail drawer is open, as an id rather than a copy — so an
	 * edit made here, or arriving over the socket from somebody else, is reflected
	 * in the drawer without a second source of truth to keep in step.
	 */
	detailId = $state(null);

	#socket = null;
	#reconnectDelay = 1000;

	get inbox() {
		return this.projects.find((p) => p.is_inbox);
	}

	projectById(id) {
		return this.projects.find((p) => p.id === id);
	}

	get detailTask() {
		return this.tasks.find((t) => t.id === this.detailId) ?? null;
	}

	openDetail(id) {
		this.detailId = id;
	}

	closeDetail() {
		this.detailId = null;
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
			case 'task.moved':
				this.upsert(task);
				break;
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
	 * Swaps one task for a new version of it. **The single place a task in the list
	 * is changed** — the optimistic updates below, the reconciliation after them,
	 * the rollbacks, the websocket, and the drag handlers in BoardView and TaskList
	 * all come through here.
	 *
	 * It builds a new array rather than writing `tasks[i] = next`. In-place index
	 * assignment did not reach the views: a ticked-off task kept its row, and a
	 * change arriving over the socket did not show at all, while `push` and whole
	 * -array assignment both worked. Rather than depend on which mutations the
	 * reactivity happens to see through, every write here produces a new array,
	 * which it certainly does see. The lists are at most a few hundred rows; the
	 * copy is not worth a moment's thought next to the class of bug it removes.
	 */
	replace(id, next) {
		this.tasks = this.tasks.map((t) => (t.id === id ? next : t));
	}

	/**
	 * Adds a task, or replaces it if the list already has it.
	 *
	 * Adding is never unconditional, because the same task arrives twice: once as
	 * the response to the request that created it, and once over the socket, which
	 * publishes to the whole project including whoever did it. Either can land
	 * first. Appending both puts two rows with one id into an `{#each}` keyed by
	 * id, which is not a duplicated row but a thrown error that stops the view
	 * rendering.
	 */
	upsert(task) {
		if (!task?.id) return;
		if (this.tasks.some((t) => t.id === task.id)) this.replace(task.id, task);
		else this.tasks = [...this.tasks, task];
	}

	get(id) {
		return this.tasks.find((t) => t.id === id);
	}

	/**
	 * Ticks a task off. The row is struck through and gone before the request
	 * leaves — which is the single most-repeated action in the app, and the one
	 * place latency would be felt all day.
	 */
	async complete(id) {
		await this.#optimistic(id, { completed: true }, () => api.completeTask(id));
	}

	async reopen(id) {
		await this.#optimistic(id, { completed: false }, () => api.reopenTask(id));
	}

	async update(id, patch) {
		await this.#optimistic(id, patch, () => api.updateTask(id, patch));
	}

	/**
	 * Applies `patch` at once, then reconciles with whatever the server says.
	 *
	 * The rollback restores the version captured before the change rather than
	 * inverting the patch: the server may have declined for a reason that has
	 * nothing to do with the fields sent, and putting back what was there is the
	 * only correction that is right in every case.
	 */
	async #optimistic(id, patch, request) {
		const previous = this.get(id);
		if (!previous) return;

		this.replace(id, { ...previous, ...patch });
		try {
			this.replace(id, await request());
		} catch (e) {
			this.replace(id, previous);
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
			this.upsert(task);
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
			this.projects = [...this.projects, project];
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
