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
import { i18n, t } from './i18n.svelte.js';

class AppState {
	user = $state(null);
	projects = $state([]);
	/** The foldable headings over the projects in the sidebar. */
	groups = $state([]);
	/**
	 * Everybody you share a project with, and yourself.
	 *
	 * Loaded once so a task row can put a name on an `assignee_id` without a
	 * request per row — and without every list having to know which project each
	 * task belongs to in order to ask for its members.
	 */
	people = $state([]);
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
	/** Bumped when a note moves, so a list showing them knows to look again. */
	notesChanged = $state(0);

	#socket = null;
	#reconnectDelay = 1000;

	get inbox() {
		return this.projects.find((p) => p.is_inbox);
	}

	projectById(id) {
		return this.projects.find((p) => p.id === id);
	}

	personById(id) {
		return this.people.find((p) => p.id === id) ?? null;
	}

	/**
	 * The person a task is waiting on, or null when it is nobody or yourself.
	 *
	 * Yourself is nobody, on purpose: every row in your own list would otherwise
	 * carry your own face, which is a column of the same initial telling you
	 * nothing. The point of showing an assignee is that it is *not* you.
	 */
	assigneeOf(task) {
		const id = task?.assignee_id;
		if (!id || id === this.user?.id) return null;
		return this.personById(id) ?? { id, name: 'Ukendt', avatar_color: '#8a8f98' };
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
			// The interface's language follows the account, so it is set the moment
			// the session is known — before anything has drawn a string.
			i18n.set(this.user.locale);
			const [{ projects }, { groups }, { people }] = await Promise.all([
				api.listProjects(),
				api.listProjectGroups(),
				api.people()
			]);
			this.projects = projects;
			this.groups = groups;
			this.people = people;
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
				this.refreshCounts();
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

			case 'project_group.created':
			case 'project_group.updated':
				this.upsertGroup(task);
				break;
			case 'project_group.deleted':
				// The projects filed under it are not deleted with it; they come back
				// out as ungrouped, which the sidebar works out from the group being
				// gone rather than from a second event per project.
				this.groups = this.groups.filter((g) => g.id !== task?.id);
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

	/**
	 * Loads a slice of tasks into the store.
	 *
	 * Closed ones come along when the view is showing them — asked for here rather
	 * than at every call site, so a view added later cannot forget and quietly
	 * ignore the setting. A caller that genuinely wants only what is open passes
	 * `completed: 'exclude'`.
	 */
	async loadTasks(params) {
		const { tasks } = await api.listTasks({
			completed: completedView.shown ? 'include' : undefined,
			...params
		});
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
		// Before the branch, not after it. A task that is already in the list can
		// still change the counts — moved to another project, finished in another
		// tab, a recurring one that closed and opened itself — and the branch below
		// returns early for exactly those.
		this.refreshCounts();
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
	/**
	 * Closes a task, and offers to put it back.
	 *
	 * Completing is one click on a small circle next to a row you were only
	 * reading, and it takes the row off the screen — so the mistake and the
	 * evidence of it leave together. Somebody who clicks the wrong one is left with
	 * no name to search for and no list to find it in.
	 *
	 * The undo is offered where the mistake happened rather than filed somewhere to
	 * be looked up. It is the same call `reopen` makes; the only new thing is that
	 * the interface says it is available.
	 *
	 * A recurring task is deliberately not offered one. Completing it moves it to
	 * its next date rather than closing it, so there is nothing to undo — and an
	 * "undo" that silently rolled the date back would be a different act from the
	 * one it claims to reverse.
	 */
	async complete(id) {
		const task = this.get(id);
		const repeats = Boolean(task?.recurrence_rule);
		const what = task?.content ?? '';

		await this.#optimistic(id, { completed: true }, () => api.completeTask(id));
		this.refreshCounts();

		if (!repeats) {
			this.toast(t('task.completedUndo', { what: truncate(what) }), {
				action: t('task.undo'),
				onaction: () => this.reopen(id)
			});
		}
	}

	async reopen(id) {
		await this.#optimistic(id, { completed: false }, () => api.reopenTask(id));
		this.refreshCounts();
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

	/**
	 * Moves a task into another project, which is what dropping it on the sidebar
	 * means.
	 *
	 * Through `move` rather than a `project_id` on an update: `sort_order` belongs
	 * to the project, so a task arriving in a new one needs a place among its
	 * tasks. The section goes too — sections belong to the project it is leaving,
	 * and carrying the id across would file it under a heading that is not there.
	 */
	async moveToProject(id, projectId) {
		const previous = this.get(id);
		if (!previous || previous.project_id === projectId) return;

		this.replace(id, { ...previous, project_id: projectId, section_id: '' });
		try {
			this.replace(id, await api.moveTask(id, { project_id: projectId, section_id: '' }));
		} catch (e) {
			this.replace(id, previous);
			this.toast(humanMessage(e));
		}
	}

	/** Puts a task on a day — dropping it on one, in Kommende or in a month grid. */
	/**
	 * Flytter en opgave til en dag — og afgør, hvad der sker med klokkeslættet.
	 *
	 * Serveren læser `due_date` og `due_time` sammen: kommer datoen uden en tid,
	 * *ryddes* tiden. Det er den rigtige regel for et felt, man skriver i, og den
	 * forkerte for et træk i en kalender — en opgave, der stod klokken 14, mistede
	 * sit klokkeslæt ved at blive trukket til dagen efter, uden at nogen bad om
	 * det, og uden at noget sagde det.
	 *
	 * Så tiden siges nu højt, tre måder:
	 *
	 *   time === undefined  behold den, opgaven har
	 *   time === ''         ryd den — det er heldagsbåndet
	 *   time === 'HH:MM'    sæt den — det er et sted på døgnet
	 *
	 * `undefined` og `''` er med vilje to forskellige ting her. De ligner hinanden
	 * i JavaScript, og det er præcis derfor de skal skilles ad ét sted frem for at
	 * blive forvekslet fire steder.
	 */
	async reschedule(id, date, time) {
		const previous = this.get(id);
		if (!previous) return;

		const clock =
			time === undefined ? (previous.due_datetime ? clockOf(previous.due_datetime) : '') : time;
		if (previous.due_date === date && (previous.due_datetime ? clockOf(previous.due_datetime) : '') === clock) {
			return;
		}
		await this.update(id, { due_date: date, due_time: clock });
	}

	/**
	 * Sets how long a task is, in minutes. Nothing else about it moves — the date
	 * and time are left alone, so making a task longer on the calendar does not
	 * also reschedule it.
	 */
	/**
	 * Snoozes a task until an ISO time, or wakes it when `until` is empty. Nothing
	 * else about the task moves — a snooze is about when you want to see it, not when
	 * it is due. Optimistic, so it greys and sinks at once.
	 */
	async snooze(id, until) {
		await this.#optimistic(
			id,
			{ snoozed_until: until || undefined },
			() => api.snoozeTask(id, until || '')
		);
	}

	async resize(id, minutes) {
		const previous = this.get(id);
		if (!previous) return;
		const rounded = Math.max(15, Math.round(minutes / 15) * 15);
		if (previous.duration_min === rounded) return;
		await this.update(id, { duration_min: rounded });
	}

	async remove(id) {
		const previous = [...this.tasks];
		this.tasks = this.tasks.filter((t) => t.id !== id);
		try {
			await api.deleteTask(id);
			this.refreshCounts();
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

	/** Files a note under a project, which is what sharing it means. */
	async moveNoteToProject(noteID, projectID) {
		try {
			await api.updateNote(noteID, { project_id: projectID });
			this.notesChanged++;
			this.toast(t('notes.filed'));
		} catch (e) {
			this.toast(humanMessage(e));
		}
	}

	async refreshProjects() {
		const { projects } = await api.listProjects();
		this.projects = projects;
	}

	/**
	 * Brings the sidebar's per-project counts back in line after tasks have moved.
	 *
	 * The count is open tasks now, which changes on every add, finish and delete —
	 * where the old one, how many people are in it, changed about twice a year. It
	 * would be quicker to add and subtract locally, and it would be wrong within a
	 * day: a task moved between projects, one finished in another tab, a recurring
	 * one that closed and reopened itself. Asking the server is one request and
	 * cannot drift.
	 *
	 * Coalesced, because a paste of five tasks is five calls in the same tick and
	 * the answer to all of them is the same.
	 */
	#countsRunning = false;
	#countsAgain = false;
	#countsTimer = null;

	refreshCounts() {
		// Two windows to coalesce over, not one. Collapsing calls that arrive within
		// a tick is the easy half; the half that matters is a change arriving while
		// the request is already in flight. Dropping those left the sidebar showing
		// the count as it was one task ago, which is the state the test caught and
		// which nothing but a reload would have corrected.
		if (this.#countsRunning) {
			this.#countsAgain = true;
			return;
		}
		clearTimeout(this.#countsTimer);
		this.#countsTimer = setTimeout(async () => {
			this.#countsRunning = true;
			try {
				const { projects } = await api.listProjects();
				const counts = new Map(projects.map((p) => [p.id, p.open_count]));
				this.projects = this.projects.map((p) =>
					counts.has(p.id) ? { ...p, open_count: counts.get(p.id) } : p
				);
			} catch {
				// The number is a convenience; failing to update it must not put an
				// error in front of somebody who was only ticking something off.
			} finally {
				this.#countsRunning = false;
			}
			if (this.#countsAgain) {
				this.#countsAgain = false;
				this.refreshCounts();
			}
		}, 120);
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

	// --- project groups -----------------------------------------------------------

	/** The same new-array upsert the projects need, and for the same reason. */
	upsertGroup(group) {
		if (!group?.id) return;
		this.groups = this.groups.some((g) => g.id === group.id)
			? this.groups.map((g) => (g.id === group.id ? group : g))
			: [...this.groups, group];
	}

	async createGroup(name) {
		try {
			const group = await api.createProjectGroup(name);
			this.upsertGroup(group);
			return group;
		} catch (e) {
			this.toast(humanMessage(e));
			return null;
		}
	}

	/**
	 * Folds or unfolds a group.
	 *
	 * Applied first and saved after, like everything else here — a fold that waits
	 * for a round trip feels like a click that did not land. The rollback matters
	 * more than usual: this is stored on the account rather than in the browser, so
	 * a failed save that left the arrow turned would be a lie that survives a
	 * reload.
	 */
	async toggleGroup(id) {
		const group = this.groups.find((g) => g.id === id);
		if (!group) return;
		await this.#patchGroup(id, { collapsed: !group.collapsed });
	}

	/**
	 * Folds or unfolds one of the sidebar's fixed headings — Projekter, Delt med
	 * mig, Filtre, Etiketter.
	 *
	 * Kept on the account, exactly like a project group's `collapsed`, and for the
	 * same reason: folding a heading says "this is the part of my work I am not in
	 * right now", which is true on the laptop and on the desktop both. The
	 * sidebar's *width* is the opposite case and stays in localStorage.
	 *
	 * Optimistic, with a rollback, for the same reason the group toggle is: waiting
	 * on a round trip to turn an arrow feels like a click that did not land — and
	 * because this is on the account, a failed save that left it turned would be a
	 * lie that survives a reload.
	 */
	async toggleSection(key) {
		const before = this.user?.sidebar_collapsed ?? [];
		const after = before.includes(key) ? before.filter((k) => k !== key) : [...before, key];

		this.user = { ...this.user, sidebar_collapsed: after };
		try {
			await api.setSidebarSections(after);
		} catch (e) {
			this.user = { ...this.user, sidebar_collapsed: before };
			this.toast(humanMessage(e));
		}
	}

	/**
	 * The order of the fixed views, as keys.
	 *
	 * Unknown keys are dropped and missing ones appended, so the stored order never
	 * has to be migrated when a view is added or taken away — which has already
	 * happened once, when Færdige left the sidebar.
	 */
	navOrder(keys) {
		const saved = this.user?.nav_order ?? [];
		const known = new Set(keys);
		const ordered = saved.filter((k) => known.has(k));
		return [...ordered, ...keys.filter((k) => !ordered.includes(k))];
	}

	async setNavOrder(order) {
		const before = this.user?.nav_order ?? [];
		this.user = { ...this.user, nav_order: order };
		try {
			await api.setNavOrder(order);
		} catch (e) {
			this.user = { ...this.user, nav_order: before };
			this.toast(humanMessage(e));
		}
	}

	sectionCollapsed(key) {
		return (this.user?.sidebar_collapsed ?? []).includes(key);
	}

	async renameGroup(id, name) {
		await this.#patchGroup(id, { name });
	}

	async setGroupColor(id, color) {
		await this.#patchGroup(id, { color });
	}

	async #patchGroup(id, patch) {
		const previous = this.groups.find((g) => g.id === id);
		if (!previous) return;

		this.upsertGroup({ ...previous, ...patch });
		try {
			this.upsertGroup(await api.updateProjectGroup(id, patch));
		} catch (e) {
			this.upsertGroup(previous);
			this.toast(humanMessage(e));
		}
	}

	/**
	 * Deletes the heading. The projects come back out as ungrouped, which is done
	 * here too rather than waiting for a re-read: the sidebar reads `group_id` to
	 * decide where a project sits, and a project pointing at a group that is gone
	 * would otherwise vanish from the list entirely until the next load.
	 */
	async deleteGroup(id) {
		const previousGroups = this.groups;
		const previousProjects = this.projects;

		this.groups = this.groups.filter((g) => g.id !== id);
		this.projects = this.projects.map((p) => (p.group_id === id ? { ...p, group_id: '' } : p));
		try {
			await api.deleteProjectGroup(id);
		} catch (e) {
			this.groups = previousGroups;
			this.projects = previousProjects;
			this.toast(humanMessage(e));
		}
	}

	async reorderGroups(ids) {
		const previous = this.groups;
		const rank = new Map(ids.map((id, i) => [id, i]));

		this.groups = this.groups.map((g) =>
			rank.has(g.id) ? { ...g, sort_order: rank.get(g.id) } : g
		);
		try {
			await api.reorderProjectGroups(ids);
		} catch (e) {
			this.groups = previous;
			this.toast(humanMessage(e));
		}
	}

	/** Marks a project with one of the palette's colours. */
	async setProjectColor(projectId, color) {
		const previous = this.projects.find((p) => p.id === projectId);
		if (!previous || previous.color === color) return;

		this.upsertProject({ ...previous, color });
		try {
			this.upsertProject(await api.updateProject(projectId, { color }));
		} catch (e) {
			this.upsertProject(previous);
			this.toast(humanMessage(e));
		}
	}

	/** Files a project under a group, or takes it out of one when groupId is ''. */
	async setProjectGroup(projectId, groupId) {
		const previous = this.projects.find((p) => p.id === projectId);
		// `?? ''` because the server omits the field when there is no group, so
		// "none" arrives as undefined and is compared against the '' that means the
		// same thing.
		if (!previous || (previous.group_id ?? '') === groupId) return;

		this.upsertProject({ ...previous, group_id: groupId });
		try {
			this.upsertProject(await api.updateProject(projectId, { group_id: groupId }));
		} catch (e) {
			this.upsertProject(previous);
			this.toast(humanMessage(e));
		}
	}

	// --- toasts -------------------------------------------------------------------

	/**
	 * Says something, and optionally offers one thing to do about it.
	 *
	 * An action gets longer on screen than a plain message: five seconds is enough
	 * to read "could not save", and not enough to notice you closed the wrong task,
	 * decide, and reach the mouse.
	 */
	toast(message, { action, onaction } = {}) {
		const id = Math.random().toString(36).slice(2);
		this.toasts.push({ id, message, action, onaction });
		setTimeout(
			() => {
				this.toasts = this.toasts.filter((t) => t.id !== id);
			},
			action ? 12000 : 5000
		);
	}

	dismissToast(id) {
		this.toasts = this.toasts.filter((t) => t.id !== id);
	}
}

/**
 * What to call a project on screen.
 *
 * Almost always its own name — somebody wrote it and it is theirs. The Inbox is the
 * exception: the server names it at account creation, before anybody has seen a
 * settings page, so it is the one project name nobody chose. Leaving it as stored
 * meant an account created in Danish said "Indbakke" in an English interface, which
 * reads as a string somebody forgot to translate. It was.
 *
 * Only while it still carries a name the server gave it. Rename it and that is your
 * name for it, in every language — the same rule as any other project.
 */
/**
 * Klokkeslættet på en opgave, som "HH:MM" i den maskine, der ser på det.
 *
 * `Date` og ikke en strengudskæring: `due_datetime` kommer som UTC med et Z på,
 * og de fjorten på et stempel er ikke klokken fjorten for den, der læser det.
 * TaskRow viser tiden på nøjagtig samme måde, og de to steder skal vise det
 * samme tal.
 */
export function clockOf(stamp) {
	const at = new Date(stamp);
	if (Number.isNaN(at.getTime())) return '';
	return `${String(at.getHours()).padStart(2, '0')}:${String(at.getMinutes()).padStart(2, '0')}`;
}

const SYSTEM_INBOX_NAMES = new Set(['indbakke', 'inbox']);

export function projectName(project) {
	if (!project) return '';
	if (project.is_inbox && SYSTEM_INBOX_NAMES.has(project.name.trim().toLowerCase())) {
		return t('nav.inbox');
	}
	return project.name;
}

/** Enough of a task's title to recognise it, without a toast the width of the page. */
function truncate(text, max = 40) {
	const trimmed = text.trim();
	return trimmed.length > max ? trimmed.slice(0, max - 1).trimEnd() + '…' : trimmed;
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
 * Whether Kommende shows the next seven days as a list, one week as a grid, or the
 * whole month.
 *
 * In localStorage rather than on the account, and for the same reason as the
 * sidebar's width: a grid of seven columns needs room, and the answer on a phone is
 * not the answer on a wide monitor. A project keeps its `view_mode` on the row
 * because that is a fact about the project — this is a fact about the screen.
 *
 * `calendar` still means the month. It is the value already in people's
 * localStorage, and renaming it would silently reset everybody who had chosen it.
 */
const UPCOMING_MODES = ['list', 'week', 'calendar'];

class UpcomingView {
	mode = $state(read());

	set(next) {
		if (!UPCOMING_MODES.includes(next)) return;
		this.mode = next;
		try {
			localStorage.setItem('verdande:upcoming', next);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
}

function read() {
	if (typeof localStorage === 'undefined') return 'list';
	const stored = localStorage.getItem('verdande:upcoming');
	return UPCOMING_MODES.includes(stored) ? stored : 'list';
}

export const upcomingView = new UpcomingView();

/**
 * Month or week on Kalender.
 *
 * Its own key rather than Kommende's, even though the two switchers look alike.
 * They are not the same choice: Kommende opens on a list of the next seven days
 * and offers a grid, and Kalender is the grid — sharing the value would mean
 * choosing a week on one and finding the other had changed, and one of them would
 * have to answer for the `list` mode the other does not have.
 *
 * In localStorage rather than on the account, and for the same reason as the
 * sidebar's width: a grid of seven columns needs room, and the answer on a phone
 * is not the answer on a wide monitor.
 */
const CALENDAR_MODES = ['week', 'month'];

class CalendarViewMode {
	mode = $state('month');

	// Nøglen som argument frem for som felt på en underklasse.
	//
	// Den vej blev prøvet og virkede ikke: en underklasses felter sættes *efter*
	// basens konstruktør er kørt, så en læsning i konstruktøren brugte basens nøgle
	// og skrivningen bagefter underklassens. Valget blev gemt ét sted og læst et
	// andet, og en uge, man havde valgt, var en måned igen efter en genindlæsning.
	// Et argument har ingen rækkefølge at tage fejl af.
	constructor(storageKey = 'verdande:calendar') {
		this.storageKey = storageKey;
		this.mode = readCalendarMode(storageKey);
	}

	set(next) {
		if (!CALENDAR_MODES.includes(next)) return;
		this.mode = next;
		try {
			localStorage.setItem(this.storageKey, next);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
}

function readCalendarMode(key) {
	if (typeof localStorage === 'undefined') return 'month';
	const stored = localStorage.getItem(key);
	return CALENDAR_MODES.includes(stored) ? stored : 'month';
}

export const calendarView = new CalendarViewMode();

/**
 * Måned eller uge på et projekts kalender.
 *
 * Sin egen nøgle, af samme grund som Kalenders er sin egen: det er tre steder med
 * hver sit spørgsmål. Kommende åbner på en liste over de næste syv dage, Kalender
 * *er* gitteret, og et projekts kalender er en del af en side, der også kan være
 * en liste og et board. At dele værdien ville betyde, at man valgte en uge ét sted
 * og fandt et andet ændret.
 *
 * Og ikke på projektet. `view_mode` er projektets — den er delt med alle, der kan
 * se det — mens uge eller måned er et spørgsmål om, hvor bred skærmen er, foran
 * hvilken der sidder én person. Det er den samme grund, som sidebjælkens bredde
 * ligger i localStorage af.
 */
export const projectCalendarView = new CalendarViewMode('verdande:project-calendar');

/**
 * Whether the lists show what has already been finished.
 *
 * In localStorage rather than on the account, like the sidebar's width and unlike
 * a folded group. It is a way of *looking* rather than a statement about the work:
 * somebody reviewing what they got done this week wants it on for ten minutes, and
 * a setting that followed them to every device would be a setting they had to turn
 * off twice.
 *
 * Off by default, and deliberately. A task list is what is left to do; a list that
 * opens full of things already done is a list that has to be read past before it
 * can be used.
 */
class CompletedView {
	shown = $state(
		typeof localStorage !== 'undefined' && localStorage.getItem('verdande:completed') === 'show'
	);

	toggle() {
		this.shown = !this.shown;
		try {
			localStorage.setItem('verdande:completed', this.shown ? 'show' : 'hide');
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
}

export const completedView = new CompletedView();


/**
 * The themes on offer.
 *
 * `dark` is the tokens on bare :root; the rest are [data-theme] blocks in
 * app.css. The list lives here so the picker and the stylesheet cannot drift —
 * an entry with no CSS behind it renders as the default and looks like a bug in
 * the picker rather than a missing block.
 */
export const THEMES = [
	{ id: 'dark', name: 'theme.dark', note: 'theme.darkNote', dark: true },
	{ id: 'charcoal', name: 'theme.charcoal', note: 'theme.charcoalNote', dark: true },
	{ id: 'dusk', name: 'theme.dusk', note: 'theme.duskNote', dark: true },
	{ id: 'light', name: 'theme.light', note: 'theme.lightNote', dark: false },
	{ id: 'paper', name: 'theme.paper', note: 'theme.paperNote', dark: false },
	{ id: 'contrast', name: 'theme.contrast', note: 'theme.contrastNote', dark: false }
];

/**
 * The looks: how it reads, as opposed to how bright it is.
 *
 * Separate from the themes because they are separate questions. Somebody who
 * wants a serif face at night should not have to give up the dark palette to get
 * one, and the two multiply out to fifteen appearances from eight small blocks of
 * CSS rather than needing one block each.
 */
export const LOOKS = [
	{ id: 'verdande', name: 'look.verdande', note: 'look.verdandeNote' },
	{ id: 'rolig', name: 'look.rolig', note: 'look.roligNote' },
	{ id: 'taet', name: 'look.taet', note: 'look.taetNote' },
	{ id: 'terminal', name: 'look.terminal', note: 'look.terminalNote' }
];

class Look {
	current = $state(
		typeof document === 'undefined'
			? 'verdande'
			: (document.documentElement.dataset.look ?? 'verdande')
	);

	set(id) {
		if (!LOOKS.some((l) => l.id === id)) return;
		this.current = id;
		// The default carries no attribute, so the plain tokens are the plain tokens
		// and there is nothing to override them back to.
		if (id === 'verdande') delete document.documentElement.dataset.look;
		else document.documentElement.dataset.look = id;
		try {
			localStorage.setItem('verdande:look', id);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
}

export const look = new Look();

/**
 * The font-size steps, shared by the menu and the body-text controls. `default`
 * carries no attribute, so the base tokens in app.css stand unchanged.
 */
export const SIZES = [
	{ id: 'small', name: 'size.small' },
	{ id: 'default', name: 'size.default' },
	{ id: 'large', name: 'size.large' },
	{ id: 'xl', name: 'size.xl' }
];

/**
 * A font-size preference — the same shape as Look: kept in localStorage and
 * mirrored onto <html>, where app.html sets it before first paint so a chosen size
 * does not flash in at the default and resize. Two instances below, because the
 * menu and the body text are separate questions.
 */
class Sizing {
	current = $state('default');
	#key;
	#attr;

	constructor(key, attr) {
		this.#key = key;
		this.#attr = attr;
		if (typeof document !== 'undefined') {
			this.current = document.documentElement.dataset[attr] ?? 'default';
		}
	}

	set(id) {
		if (!SIZES.some((s) => s.id === id)) return;
		this.current = id;
		if (id === 'default') delete document.documentElement.dataset[this.#attr];
		else document.documentElement.dataset[this.#attr] = id;
		try {
			localStorage.setItem(this.#key, id);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}
}

export const menuSize = new Sizing('verdande:menu-size', 'menuSize');
export const textSize = new Sizing('verdande:text-size', 'textSize');

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
