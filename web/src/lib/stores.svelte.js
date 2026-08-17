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

	/**
	 * Bumped whenever a label changes anywhere.
	 *
	 * A counter rather than the labels themselves: the sidebar wants each label's
	 * task count, which no single event carries and which changes whenever a task
	 * gains or loses one. Whoever is showing labels re-reads them; that is one
	 * small request against modelling a count in two places.
	 */
	labelsChanged = $state(0);

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

			// Projects and labels belong to a person rather than to a project, so
			// they arrive on the user's own channel — a project that has just been
			// created has no subscribers, because nobody was watching a thing that
			// did not exist a moment ago.
			case 'project.created':
			case 'project.updated':
				this.upsertProject(task);
				break;
			case 'project.deleted':
				this.projects = this.projects.filter((p) => p.id !== task?.id);
				break;

			// The payload is not always the label — a rename says only that
			// something changed — and the sidebar wants counts anyway, which no
			// single event carries. Asking is cheaper than modelling it.
			case 'label.changed':
				this.labelsChanged++;
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
		// A task can invent a label just by mentioning one — "@regnskab" in quick
		// add creates it — and no label event is sent for that, because it happened
		// inside a task write. Comparing the two lists is what keeps the sidebar
		// honest without re-reading the labels on every completed checkbox.
		const before = this.tasks.find((t) => t.id === id)?.labels ?? [];
		if ((next?.labels ?? []).join() !== before.join()) this.labelsChanged++;

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
		if (this.tasks.some((t) => t.id === task.id)) {
			this.replace(task.id, task);
			return;
		}
		if (task.labels?.length) this.labelsChanged++;
		this.tasks = [...this.tasks, task];
	}

	get(id) {
		return this.tasks.find((t) => t.id === id);
	}

	/**
	 * Adds a project, or replaces it if the list already has it.
	 *
	 * The same guard the tasks need, and for the same reason: a project arrives
	 * twice, once as the response to the request that created it and once over
	 * the socket. Appending both puts two rows with one id into a keyed `{#each}`,
	 * which throws and stops the sidebar rendering.
	 */
	upsertProject(project) {
		if (!project?.id) return;
		this.projects = this.projects.some((p) => p.id === project.id)
			? this.projects.map((p) => (p.id === project.id ? project : p))
			: [...this.projects, project];
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
			this.upsertProject(project);
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

	/**
	 * Puts the projects in the given order.
	 *
	 * The whole list rather than one moved item: it is a handful of rows, and
	 * sending the order you want cannot land in the half-applied state a sequence
	 * of individual moves can. `sort_order` is set here as well as on the server,
	 * so the sidebar settles immediately rather than after a round trip.
	 */
	async reorderProjects(ids) {
		const previous = this.projects;
		const rank = new Map(ids.map((id, i) => [id, i]));

		this.projects = this.projects.map((p) =>
			rank.has(p.id) ? { ...p, sort_order: rank.get(p.id) } : p
		);
		try {
			await api.reorderProjects(ids);
		} catch (e) {
			this.projects = previous;
			this.toast(humanMessage(e));
		}
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

/**
 * How wide the sidebar is, and whether it is showing at all.
 *
 * Kept in localStorage rather than on the account: it is a property of the
 * screen you are sitting at, not of you. The same person on a laptop and on a
 * wide monitor wants different answers, and syncing it would make one of those
 * two wrong every time they switch.
 */
const SIDEBAR_MIN = 200;
const SIDEBAR_MAX = 480;
const SIDEBAR_DEFAULT = 268;

function storedNumber(key, fallback) {
	if (typeof localStorage === 'undefined') return fallback;
	const raw = Number(localStorage.getItem(key));
	return Number.isFinite(raw) && raw > 0 ? raw : fallback;
}

class SidebarLayout {
	width = $state(storedNumber('verdande:sidebar-width', SIDEBAR_DEFAULT));
	collapsed = $state(
		typeof localStorage !== 'undefined' && localStorage.getItem('verdande:sidebar-collapsed') === '1'
	);

	setWidth(px) {
		// Clamped here rather than in the drag handler, so every caller gets the
		// same rule and a stored value from an older build cannot make the
		// sidebar unusably narrow.
		this.width = Math.round(Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, px)));
		this.#save('verdande:sidebar-width', String(this.width));
	}

	toggle() {
		this.collapsed = !this.collapsed;
		this.#save('verdande:sidebar-collapsed', this.collapsed ? '1' : '0');
	}

	reset() {
		this.setWidth(SIDEBAR_DEFAULT);
	}

	#save(key, value) {
		try {
			localStorage.setItem(key, value);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
}

export const sidebar = new SidebarLayout();

/**
 * The themes on offer.
 *
 * `dark` is the tokens on bare :root; the rest are [data-theme] blocks in
 * app.css. The list lives here so the picker and the stylesheet cannot drift —
 * an entry with no CSS behind it renders as the default and looks like a bug in
 * the picker rather than a missing block.
 */
export const THEMES = [
	{ id: 'dark', name: 'Nordlys', note: 'Mørk med grøn accent.', dark: true },
	{ id: 'dusk', name: 'Skumring', note: 'Mørk og varm, med rav.', dark: true },
	{ id: 'light', name: 'Dagslys', note: 'Lys og køligt neutral.', dark: false },
	{ id: 'paper', name: 'Papir', note: 'Lys og varm, som papir.', dark: false },
	{ id: 'contrast', name: 'Kontrast', note: 'Sort på hvidt, til skarpt lys.', dark: false }
];

/** Theme, kept in localStorage and applied to the document element. */
class Theme {
	/**
	 * Mirrors the attribute on <html>, which app.html sets before first paint —
	 * that is the only way to avoid a white flash on load, and it means the
	 * document, not this object, is where the truth lives.
	 */
	current = $state(
		typeof document === 'undefined' ? 'dark' : (document.documentElement.dataset.theme ?? 'dark')
	);

	set(id) {
		if (!THEMES.some((t) => t.id === id)) return;
		this.current = id;
		document.documentElement.dataset.theme = id;

		// The browser paints its own chrome from this — the address bar on a
		// phone, the frame of an installed PWA. Left alone it stays the colour of
		// whichever theme shipped in the markup.
		const ground = getComputedStyle(document.documentElement).getPropertyValue('--ground').trim();
		document.querySelector('meta[name="theme-color"]')?.setAttribute('content', ground);

		try {
			localStorage.setItem('verdande:theme', id);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}

	/**
	 * The topbar button, which flips between light and dark rather than walking
	 * all five. Somebody who has chosen Skumring wants the light one to be Papir,
	 * not to be marched back to the default — so it moves to the other side of
	 * the list and stays warm or stays cool.
	 */
	toggle() {
		const now = THEMES.find((t) => t.id === this.current) ?? THEMES[0];
		const opposite = THEMES.filter((t) => t.dark !== now.dark);
		const index = THEMES.filter((t) => t.dark === now.dark).indexOf(now);
		this.set((opposite[index] ?? opposite[0]).id);
	}
}

export const theme = new Theme();

export { ApiError, humanMessage };
