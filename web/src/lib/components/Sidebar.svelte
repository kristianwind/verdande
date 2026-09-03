<script>
	import { app, sidebar, projectName } from '$lib/stores.svelte.js';
	import { api } from '$lib/api.js';
	import { TASK, PROJECT, GROUP, NOTE, startDrag, carries, dragged, accept } from '$lib/dnd.js';
	import { COLORS, colorVar } from '$lib/colors.js';
	import { focusOnMount } from '$lib/focus.js';
	import { t } from '$lib/i18n.svelte.js';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';

	let { onnavigate } = $props();

	let adding = $state(false);
	let newName = $state('');

	async function createProject(event) {
		event.preventDefault();
		const name = newName.trim();
		if (!name) return;
		newName = '';
		adding = false;
		const project = await app.createProject(name);
		if (!project) return;
		// Closes the drawer as well as navigating. On a phone the sidebar is an
		// overlay, and creating a project moved the page underneath it while leaving
		// it covering the screen — every link in here closes it, and this makes the
		// same move for the same reason.
		onnavigate?.();
		goto(`/projekt/${project.id}`);
	}

	async function signOut() {
		await api.logout();
		location.href = '/';
	}

	let filters = $state([]);
	let labels = $state([]);

	// Reads app.labelsChanged so the effect re-runs when a label is created,
	// renamed or deleted anywhere — including in another tab, or by Claude
	// through the connector.
	$effect(() => {
		app.labelsChanged;
		api.listFilters().then((r) => (filters = r.filters)).catch(() => {});
		api.listLabels().then((r) => (labels = r.labels)).catch(() => {});
	});

	// Split by who owns it, not by whether anybody else is in it.
	//
	// The two headings say "Projekter" and "Delt med mig", and the split used to be
	// on `shared`, which only means the project has more than one member. So the
	// moment you invited somebody to a project of your own it moved under "Delt med
	// mig" — where it lost its place in the order, stopped being draggable, and
	// could not be filed under a group. Sharing your own project is not the same
	// event as somebody sharing theirs with you.
	let shared = $derived(app.projects.filter((p) => !p.is_inbox && p.role !== 'owner'));
	// Sorted here rather than relying on the order the server sent, so a drag
	// settles the moment it is dropped instead of after the round trip.
	let own = $derived(
		app.projects
			.filter((p) => !p.is_inbox && p.role === 'owner')
			.sort((a, b) => a.sort_order - b.sort_order)
	);
	let current = $derived($page.url.pathname);

	// --- groups -------------------------------------------------------------------

	let groups = $derived([...app.groups].sort((a, b) => a.sort_order - b.sort_order));
	let groupIds = $derived(new Set(groups.map((g) => g.id)));

	/**
	 * Which heading a project renders under — '' for the loose ones at the top.
	 *
	 * It is not simply `group_id`, and the difference is not paranoia: a group
	 * deleted in another tab arrives as one event, without a second one per
	 * project, so for a moment the projects here still name a heading that no
	 * longer renders. Filing by the raw id would drop them out of the sidebar
	 * entirely — under a group that is not drawn, which is nowhere.
	 */
	const bucketOf = (p) => (p.group_id && groupIds.has(p.group_id) ? p.group_id : '');

	let ungrouped = $derived(own.filter((p) => bucketOf(p) === ''));
	const inGroup = (id) => own.filter((p) => p.group_id === id);

	let addingGroup = $state(false);
	let newGroupName = $state('');
	let renamingGroup = $state(null);
	let groupName = $state('');

	async function createGroup(event) {
		event.preventDefault();
		const name = newGroupName.trim();
		if (!name) return;
		newGroupName = '';
		addingGroup = false;
		await app.createGroup(name);
	}

	async function renameGroup(event, group) {
		event.preventDefault();
		const name = groupName.trim();
		renamingGroup = null;
		if (!name || name === group.name) return;
		await app.renameGroup(group.id, name);
	}

	/** Double-click, or F2 with the row focused. The colour is chosen in the same
	 *  form, because it is the same edit to the same thing. */
	/** Cleared by a second click, which means the double was a rename. */
	let navTimer;

	function startRename(group) {
		clearTimeout(navTimer);
		renamingGroup = group.id;
		groupName = group.name;
	}


	// --- the fixed views ---------------------------------------------------------

	// Delegated is only offered where delegating is possible at all: on an instance
	// with one person that view can never have anything in it, and a permanent empty
	// entry is a question the sidebar keeps asking and answering "no".
	let navItems = $derived(
		[
			{ key: 'today', href: '/', label: 'nav.today' },
			{ key: 'upcoming', href: '/upcoming', label: 'nav.upcoming' },
			app.projects.some((p) => p.shared)
				? { key: 'delegated', href: '/uddelegeret', label: 'nav.delegated' }
				: null,
			// No href: the inbox is a project and is drawn by projectRow, which knows
			// its id. It is in this list only so it can be dragged with the others.
			{ key: 'inbox', label: 'nav.inbox' },
			// Noter hører til her, og bærer et ark frem for en prik.
			//
			// Den stod før for sig selv over en streg, hvilket sagde det rigtige og
			// kostede halvfjerds pixels: en streg, luft over og luft under, og
			// projekterne skubbet så langt ned, at de to første var alt, man så uden
			// at rulle. Grunden til at skille den ud var, at den ellers læste som
			// endnu et filter — men det, der gør den til et filter for øjet, er
			// prikken, ikke pladsen. Et andet mærke siger "en anden slags ting" og
			// koster ingenting.
			//
			// Sidst i rækken, fordi `navOrder` føjer en ukendt nøgle til bagest: står
			// den et andet sted her, ser en ny konto én rækkefølge og alle
			// eksisterende en anden.
			{ key: 'notes', href: '/noter', label: 'notes.title' },
			// Kalenderen bærer et gitter, af samme grund som Noter bærer et ark: den
			// er ikke endnu et filter over de samme opgaver. Den er de samme opgaver
			// med noget andet lagt over — Googles begivenheder — og en prik ville
			// sige, at den hørte til blandt I dag og Kommende.
			//
			// Sidst i rækken, fordi `navOrder` føjer en ukendt nøgle til bagest: står
			// den et andet sted her, ser en ny konto én rækkefølge og alle
			// eksisterende en anden.
			{ key: 'calendar', href: '/kalender', label: 'nav.calendar' }
		].filter(Boolean)
	);

	let orderedNav = $derived(
		app
			.navOrder(navItems.map((i) => i.key))
			.map((key) => navItems.find((i) => i.key === key))
			.filter(Boolean)
	);

	let draggingNav = $state(null);
	let overNav = $state(null);

	function onNavDragStart(event, key) {
		draggingNav = key;
		event.dataTransfer.effectAllowed = 'move';
		// A payload, because Firefox refuses to start a drag without one.
		event.dataTransfer.setData('text/plain', key);
	}

	function onNavDragOver(event, key) {
		if (!draggingNav || key === draggingNav) return;
		event.preventDefault();
		overNav = key;
	}

	function clearNavDrag() {
		draggingNav = null;
		overNav = null;
	}

	async function onNavDrop(event, key) {
		const moved = draggingNav;
		// Checked before anything is swallowed. Stopping the event first meant a task
		// dragged onto I dag never reached the handler that gives it a date — the row
		// took the drop and then discovered it was not for it.
		if (!moved || moved === key) {
			clearNavDrag();
			return;
		}
		event.preventDefault();
		event.stopPropagation();
		clearNavDrag();

		const keys = orderedNav.map((i) => i.key).filter((k) => k !== moved);
		keys.splice(keys.indexOf(key), 0, moved);
		await app.setNavOrder(keys);
	}

	// --- resizing -------------------------------------------------------------------

	let resizing = $state(false);

	/**
	 * Pointer events rather than mouse events, so a trackpad, a mouse and a stylus
	 * all work. Capturing the pointer means the drag keeps following even when it
	 * leaves the handle, which at 4px wide it does constantly.
	 */
	function startResize(event) {
		event.preventDefault();
		resizing = true;
		event.currentTarget.setPointerCapture(event.pointerId);
	}

	function onResize(event) {
		if (!resizing) return;
		sidebar.setWidth(event.clientX);
	}

	function endResize(event) {
		if (!resizing) return;
		resizing = false;
		event.currentTarget.releasePointerCapture?.(event.pointerId);
	}

	// --- reordering ---------------------------------------------------------------

	let draggingId = $state(null);
	let overId = $state(null);
	let overBelow = $state(false);
	/** The group whose heading is lit up as a drop target. */
	let overGroup = $state(null);
	let draggingGroup = $state(null);

	function clearDrag() {
		draggingId = null;
		draggingGroup = null;
		overId = null;
		overGroup = null;
		overProject = null;
	}

	function onDragStart(event, project) {
		draggingId = project.id;
		startDrag(event, PROJECT, project.id);
	}

	function onDragOver(event, project) {
		if (!carries(event, PROJECT) || draggingId === project.id) return;
		accept(event);

		const box = event.currentTarget.getBoundingClientRect();
		overId = project.id;
		overGroup = null;
		overBelow = event.clientY > box.top + box.height / 2;
	}

	/**
	 * Dropping onto a project puts the dragged one in that gap — and into the same
	 * group, which is what makes dragging between two headings work without a
	 * separate gesture. The order is one write and the group is another; each is
	 * meaningful on its own, and neither can leave the other half-applied.
	 */
	async function onDrop(event, target) {
		event.preventDefault();
		const id = dragged(event, PROJECT) || draggingId;
		const below = overBelow;
		clearDrag();
		if (!id || id === target.id) return;

		const moved = own.find((p) => p.id === id);
		if (!moved) return;

		const without = own.filter((p) => p.id !== id);
		const at = without.findIndex((p) => p.id === target.id);
		if (at < 0) return;

		const ordered = [...without];
		ordered.splice(below ? at + 1 : at, 0, moved);

		await app.setProjectGroup(id, bucketOf(target));
		await app.reorderProjects(ordered.map((p) => p.id));
	}

	/**
	 * Dropping onto a heading files the project under it and puts it last.
	 *
	 * Last rather than first: a heading is a wide target and dropping on it is the
	 * coarse gesture — "somewhere in here" — where dropping on a row is the precise
	 * one. Landing at the top would push whatever is already there down, which is a
	 * rearrangement nobody asked for.
	 */
	async function onDropInGroup(event, groupID) {
		event.preventDefault();
		const id = dragged(event, PROJECT) || draggingId;
		clearDrag();
		if (!id) return;

		const moved = own.find((p) => p.id === id);
		if (!moved) return;

		await app.setProjectGroup(id, groupID);

		// Everything else keeps the order it already had; the dragged project is
		// reinserted after the last one already under that heading.
		const others = own.filter((p) => p.id !== id);
		const ordered = [...others];
		ordered.splice(others.findLastIndex((p) => bucketOf(p) === groupID) + 1, 0, moved);
		await app.reorderProjects(ordered.map((p) => p.id));
	}

	// --- a task dropped on "I dag" ---------------------------------------------------

	/**
	 * "Gør det i dag" is the most-made rescheduling there is, and the sidebar is
	 * where the pointer already goes.
	 *
	 * Only this one of the two date views. "Kommende" is a range rather than a
	 * day — it starts today and runs a week — so a drop on it would have to invent
	 * a date the label does not name. The month grid inside it takes drops per
	 * cell, which is where a specific day is actually offered.
	 */
	let overToday = $state(false);

	function todayISO() {
		const now = new Date();
		return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(
			now.getDate()
		).padStart(2, '0')}`;
	}

	async function onDropOnToday(event) {
		event.preventDefault();
		const id = dragged(event, TASK);
		overToday = false;
		if (!id) return;
		await app.reschedule(id, todayISO());
	}

	// --- a task dropped on a project ------------------------------------------------

	/** The project row lit up because a task is hovering over it. */
	let overProject = $state(null);

	/**
	 * A viewer can open a project but not write to it, so it is not a place a task
	 * can be dropped. Refusing here rather than letting the server refuse is the
	 * difference between a row that does not light up and a task that visibly moves
	 * and then jumps back.
	 */
	const canReceive = (project) => project.role === 'owner' || project.role === 'editor';

	function onTaskDragOver(event, project) {
		if (!(carries(event, TASK) || carries(event, NOTE)) || !canReceive(project)) return;
		accept(event);
		overProject = project.id;
	}

	/**
	 * Et slip på et projekt, uanset hvad der bliver sluppet.
	 *
	 * Rækken kaldte den her for opgaver og lod noter falde ned i grenen, der
	 * omarrangerer projekter — hvor en note ikke betyder noget. Samtidig tog
	 * `onTaskDragOver` imod begge, så rækken lyste op, når en note blev trukket hen
	 * over den. Den værst mulige form for i stykker: den så ud, som om den ville
	 * virke, helt indtil man slap.
	 */
	async function onTaskDrop(event, project) {
		event.preventDefault();
		overProject = null;
		if (!canReceive(project)) return;

		// The same target takes both. A note dropped on a project is filed there,
		// which is also how it gets shared — one gesture, one meaning, and the
		// sidebar already reads as "put this here".
		// Nyttelasten er ét eller flere id'er adskilt af mellemrum. Markerer man
		// halvtreds noter og trækker dem herhen, hører de alle sammen til det ene
		// sted, man slap dem — det er én handling, uanset hvor mange den flytter.
		const noteIDs = (dragged(event, NOTE) ?? '').split(' ').filter(Boolean);
		if (noteIDs.length) {
			for (const id of noteIDs) await app.moveNoteToProject(id, project.id);
			return;
		}

		const id = dragged(event, TASK);
		if (!id) return;
		await app.moveToProject(id, project.id);
	}

	function onGroupDragStart(event, group) {
		draggingGroup = group.id;
		startDrag(event, GROUP, group.id);
	}

	/**
	 * A heading takes two kinds of drop, so it has to say which one it is showing.
	 * A project joins the group; another heading reorders against it.
	 */
	function onGroupDragOver(event, group) {
		if (carries(event, GROUP)) {
			if (draggingGroup === group.id) return;
			accept(event);
			const box = event.currentTarget.getBoundingClientRect();
			overId = group.id;
			overGroup = null;
			overBelow = event.clientY > box.top + box.height / 2;
			return;
		}
		if (!carries(event, PROJECT)) return;
		accept(event);
		overGroup = group.id;
		overId = null;
	}

	async function onGroupDrop(event, group) {
		if (carries(event, GROUP)) {
			event.preventDefault();
			const id = dragged(event, GROUP) || draggingGroup;
			const below = overBelow;
			clearDrag();
			if (!id || id === group.id) return;

			const without = groups.filter((g) => g.id !== id);
			const at = without.findIndex((g) => g.id === group.id);
			if (at < 0) return;
			const ordered = [...without];
			ordered.splice(below ? at + 1 : at, 0, groups.find((g) => g.id === id));
			await app.reorderGroups(ordered.map((g) => g.id));
			return;
		}
		await onDropInGroup(event, group.id);
	}
</script>

<nav class="sidebar" aria-label={t('nav.main')}>
	<div class="brand">
		<!-- Verdande's mark: the rune Wunjo, which is the letter the Norn's name
		     starts with in the elder futhark. One glyph, no wordmark beside it —
		     the name is in the tab and everywhere else already. -->
		<span class="rune" aria-hidden="true">ᚹ</span>
		<span class="name">verdande</span>
	</div>

	<div class="views">
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<!-- The fixed views, in the order this person put them.
		     Drawn from a list rather than written out, because they can be reordered
		     now and a hand-written run cannot be. Unknown keys are dropped and new
		     ones appended, so adding a view later does not strand anybody's order. -->
		{#each orderedNav as item (item.key)}
			{#if item.key === 'inbox'}
				{#if app.inbox}
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<div
						class="navrow"
						class:dragging={draggingNav === item.key}
						class:over={overNav === item.key}
						ondragover={(e) => onNavDragOver(e, item.key)}
						ondragleave={() => (overNav = null)}
						ondrop={(e) => onNavDrop(e, item.key)}
					>
						{@render grip(item.key)}
						{@render projectRow(app.inbox, false)}
					</div>
				{/if}
			{:else}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div
					class="navrow"
					class:dragging={draggingNav === item.key}
					class:over={overNav === item.key}
					ondragover={(e) => onNavDragOver(e, item.key)}
					ondragleave={() => (overNav = null)}
					ondrop={(e) => onNavDrop(e, item.key)}
				>
					{@render grip(item.key)}
					<!-- I dag also takes a task dropped on it — the most-made rescheduling
					     there is, and the sidebar is where the pointer already is. The
					     handlers were lost when this run became a list; the test caught
					     it, which is the second time that drop has earned its test. -->
					<a
						href={item.href}
						data-view={item.key}
						class:active={current === item.href}
						class:receiving={item.key === 'today' && overToday}
						onclick={onnavigate}
						ondragover={(e) => {
							if (item.key !== 'today' || !carries(e, TASK)) return;
							accept(e);
							overToday = true;
						}}
						ondragleave={() => (overToday = false)}
						ondrop={(e) => item.key === 'today' && onDropOnToday(e)}
					>
						{#if item.key === 'notes'}
							<svg class="mark" viewBox="0 0 16 16" aria-hidden="true">
								<path
									d="M4 1.5h5.2L12.5 5v9.5H4z"
									fill="none"
									stroke="currentColor"
									stroke-width="1.4"
									stroke-linejoin="round"
								/>
								<path d="M9 1.7V5h3.3" fill="none" stroke="currentColor" stroke-width="1.4" />
							</svg>
						{:else if item.key === 'calendar'}
							<svg class="mark" viewBox="0 0 16 16" aria-hidden="true">
								<rect
									x="2.2"
									y="3.2"
									width="11.6"
									height="10.6"
									rx="1.4"
									fill="none"
									stroke="currentColor"
									stroke-width="1.4"
								/>
								<path
									d="M2.2 6.4h11.6M5.6 2.2v2M10.4 2.2v2"
									fill="none"
									stroke="currentColor"
									stroke-width="1.4"
									stroke-linecap="round"
								/>
							</svg>
						{:else if item.key === 'today'}
							<!-- A star, the way Things marks Today: the one view you open first. -->
							<svg class="mark" viewBox="0 0 16 16" aria-hidden="true">
								<path
									d="M8 2l1.7 3.6 3.9.5-2.9 2.7.7 3.9L8 10.9 4.6 13.4l.7-3.9L2.4 6.1l3.9-.5z"
									fill="none"
									stroke="currentColor"
									stroke-width="1.3"
									stroke-linejoin="round"
								/>
							</svg>
						{:else if item.key === 'upcoming'}
							<!-- A calendar with a day marked — the Calendar view carries the plain
							     grid, so the marked day keeps the two apart. -->
							<svg class="mark" viewBox="0 0 16 16" aria-hidden="true">
								<rect
									x="2.2"
									y="3.2"
									width="11.6"
									height="10.6"
									rx="1.4"
									fill="none"
									stroke="currentColor"
									stroke-width="1.4"
								/>
								<path
									d="M2.2 6.4h11.6M5.6 2.2v2M10.4 2.2v2"
									fill="none"
									stroke="currentColor"
									stroke-width="1.4"
									stroke-linecap="round"
								/>
								<rect x="8.8" y="8.6" width="2.6" height="2.6" rx="0.5" fill="currentColor" />
							</svg>
						{:else if item.key === 'delegated'}
							<!-- A person: the tasks you are waiting on someone else for. -->
							<svg class="mark" viewBox="0 0 16 16" aria-hidden="true">
								<circle cx="8" cy="5.4" r="2.5" fill="none" stroke="currentColor" stroke-width="1.4" />
								<path
									d="M3.4 13.4c0-2.6 2.1-4.2 4.6-4.2s4.6 1.6 4.6 4.2"
									fill="none"
									stroke="currentColor"
									stroke-width="1.4"
									stroke-linecap="round"
								/>
							</svg>
						{:else}
							<span class="dot" class:today={item.key === 'today'} aria-hidden="true"></span>
						{/if}
						{t(item.label)}
					</a>
				</div>
			{/if}
		{/each}
	</div>

	<!-- One row for every project the sidebar shows — your own, the Inbox and the
	     shared ones — because all three are somewhere a task can be dropped, and
	     three copies of that would be three chances to fix a bug in two of them.
	     Only your own reorder: `sort_order` is a column on the project, so a shared
	     one stays where its owner put it. -->
	<!-- A handle rather than the whole row.
	     Making the row draggable made everything inside it draggable too, and the
	     sidebar is already a drop target for tasks — a task dragged onto I dag
	     stopped landing, because the row had claimed the gesture. A grip claims
	     nothing until it is grabbed. -->
	<!-- Håndtaget har ingen prik i sig længere.
	     ⠿ sad i den to pixels brede rende til venstre for teksten og lå oven i
	     rækkens egen kant — idéen var rigtig, pladsen var det ikke. Men elementet
	     bliver: det er dét, der ejer trækket, og gjorde man i stedet hele rækken
	     draggable, ville alt indeni også blive det — og en opgave trukket hen på
	     I dag holdt op med at lande, fordi rækken havde taget gesten. Nu er
	     håndtaget usynligt og lidt bredere, og hånden, der kommer ved hover,
	     er hele forklaringen. -->
	{#snippet grip(key)}
		<span
			class="grip"
			draggable="true"
			ondragstart={(e) => onNavDragStart(e, key)}
			ondragend={clearNavDrag}
			aria-hidden="true"
		></span>
	{/snippet}

	{#snippet projectRow(project, sortable)}
		<a
			href="/projekt/{project.id}"
			data-view={project.is_inbox ? 'inbox' : undefined}
			class:sortable
			class:active={current === `/projekt/${project.id}`}
			class:dragging={draggingId === project.id}
			class:drop-above={sortable && overId === project.id && !overBelow}
			class:drop-below={sortable && overId === project.id && overBelow}
			class:receiving={overProject === project.id}
			onclick={onnavigate}
			draggable={sortable}
			ondragstart={(e) => sortable && onDragStart(e, project)}
			ondragend={clearDrag}
			ondragover={(e) => {
				if (carries(e, TASK) || carries(e, NOTE)) onTaskDragOver(e, project);
				else if (sortable) onDragOver(e, project);
			}}
			ondragleave={() => {
				overId = null;
				overProject = null;
			}}
			ondrop={(e) =>
				carries(e, TASK) || carries(e, NOTE)
					? onTaskDrop(e, project)
					: sortable && onDrop(e, project)}
		>
			{#if project.is_inbox}
				<!-- A tray, not a coloured dot: the Inbox is a fixed view that happens to
				     be a project, and it reads as one of the views above, not as the first
				     of the projects below. -->
				<svg class="mark" viewBox="0 0 16 16" aria-hidden="true">
					<path
						d="M2.3 9.2L4 3.4a1.1 1.1 0 011-.8h6a1.1 1.1 0 011 .8l1.7 5.8"
						fill="none"
						stroke="currentColor"
						stroke-width="1.3"
						stroke-linejoin="round"
					/>
					<path
						d="M2.3 9.2v2.9A1.3 1.3 0 003.6 13.4h8.8a1.3 1.3 0 001.3-1.3V9.2h-3.3l-1 1.7H6.6l-1-1.7z"
						fill="none"
						stroke="currentColor"
						stroke-width="1.3"
						stroke-linejoin="round"
					/>
				</svg>
			{:else}
				<span class="dot" style="background: {colorVar(project.color)}" aria-hidden="true"></span>
			{/if}
			{projectName(project)}
			<!-- Open tasks, not people. The same grey number meant three things in this
			     one column — people at a project, projects at a group, tasks at a label
			     — and in a task app a number beside a project is read as tasks. Who
			     else is in it is on the project's own page.

			     Shown when there is something left rather than when the project is
			     shared, which is what it keyed on before: a nought beside every empty
			     project is a column of noughts. -->
			{#if project.open_count > 0}
				<span class="count">{project.open_count}</span>
			{/if}
		</a>
	{/snippet}

	<!--
		A fixed heading that folds, the same way a project group's does.

		One snippet for all four rather than four copies of a chevron and an aria
		attribute: they have to behave identically, and the group heading is already
		the proof that they drift otherwise — it grew a fold, a colour and two
		actions while these stayed plain text.

		The whole heading is the target, not the chevron. At this size a lone chevron
		is something you miss, and the name is where the eye already is.
	-->
	<!-- One monochrome mark per kind of heading, so a section and a group read as the
	     same kind of thing — a folder of projects, a funnel of filters, a tag of
	     labels — instead of one being a faint label and the other a coloured dot. -->
	{#snippet sectionIcon(key)}
		{#if key === 'filters'}
			<svg class="head-icon" viewBox="0 0 16 16" aria-hidden="true">
				<path
					d="M2.2 3.4h11.6L9.3 8.8v3.4L6.7 10.9V8.8z"
					fill="none"
					stroke="currentColor"
					stroke-width="1.4"
					stroke-linejoin="round"
				/>
			</svg>
		{:else if key === 'labels'}
			<svg class="head-icon" viewBox="0 0 16 16" aria-hidden="true">
				<path
					d="M7.7 2.3H13v5.3l-5.3 5.3-5.3-5.3z"
					fill="none"
					stroke="currentColor"
					stroke-width="1.4"
					stroke-linejoin="round"
				/>
				<circle cx="10.2" cy="4.9" r="0.95" fill="currentColor" />
			</svg>
		{:else if key === 'shared'}
			<svg class="head-icon" viewBox="0 0 16 16" aria-hidden="true">
				<circle cx="6.2" cy="5.6" r="2.1" fill="none" stroke="currentColor" stroke-width="1.4" />
				<path
					d="M2.6 12.8c0-2.1 1.7-3.3 3.6-3.3s3.6 1.2 3.6 3.3"
					fill="none"
					stroke="currentColor"
					stroke-width="1.4"
					stroke-linecap="round"
				/>
				<path
					d="M11 9.6c1.5.2 2.6 1.3 2.6 3.2"
					fill="none"
					stroke="currentColor"
					stroke-width="1.4"
					stroke-linecap="round"
				/>
				<circle cx="11.2" cy="5.6" r="1.7" fill="none" stroke="currentColor" stroke-width="1.4" />
			</svg>
		{:else}
			<!-- Projekter and every group: a folder of projects. -->
			<svg class="head-icon" viewBox="0 0 16 16" aria-hidden="true">
				<path
					d="M2 4.6a1 1 0 011-1h3.1l1.4 1.5H13a1 1 0 011 1v6.3a1 1 0 01-1 1H3a1 1 0 01-1-1z"
					fill="none"
					stroke="currentColor"
					stroke-width="1.4"
					stroke-linejoin="round"
				/>
			</svg>
		{/if}
	{/snippet}

	{#snippet foldHeading(key, label)}
		<h2 class="fold">
			<button
				onclick={() => app.toggleSection(key)}
				aria-expanded={!app.sectionCollapsed(key)}
			>
				<svg
					class="chevron"
					class:collapsed={app.sectionCollapsed(key)}
					viewBox="0 0 24 24"
					aria-hidden="true"
				>
					<path d="M6 9l6 6 6-6" />
				</svg>
				{@render sectionIcon(key)}
				{label}
			</button>
		</h2>
	{/snippet}

	<!-- Alt herunder ruller; mærket og de faste visninger gør ikke.
	     Hele sidebjælken rullede før, så runen og I dag forsvandt op over kanten,
	     så snart der var projekter nok — og de er dem, man er på vej hen til
	     oftest. En rulleboks indeni frem for `position: sticky` udenom, fordi et
	     klæbende hoved efterlader rullebjælken i fuld højde ved siden af sig: den
	     skal begynde under hovedet, ikke bag det.

	     Snippet-erklæringerne bliver liggende udenfor. De tegner ikke noget selv,
	     men de kaldes oppe fra .views, og en snippet erklæret herinde hører kun
	     til herinde. -->
	<div class="scroller">
		<div class="group">
			<!-- The heading is also the drop target for "no group": without it, a project
			     dragged into a group could only be got out again by emptying the group,
			     and with every project filed there would be no loose row left to aim at. -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="group-head sticky-head"
				class:over={overGroup === ''}
				ondragover={(e) => {
					if (!carries(e, PROJECT)) return;
					accept(e);
					overGroup = '';
					overId = null;
				}}
				ondragleave={() => (overGroup = null)}
				ondrop={(e) => onDropInGroup(e, '')}
			>
				{@render foldHeading('projects', t('sidebar.projects'))}
				<button
					class="icon"
					onclick={() => {
						adding = !adding;
						addingGroup = false;
					}}
					aria-label={t('sidebar.newProject')}>+</button
				>
				<button
					class="icon new-group"
					onclick={() => {
						addingGroup = !addingGroup;
						adding = false;
						newGroupName = '';
					}}
					aria-label={t('sidebar.newGroup')}
					title={t('sidebar.newGroup')}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" />
					</svg>
				</button>
			</div>

			{#if adding}
				<form onsubmit={createProject}>
					<input
						bind:value={newName}
						use:focusOnMount
						placeholder={t('sidebar.projectName')}
						aria-label={t('sidebar.projectName')}
						onblur={() => !newName.trim() && (adding = false)}
						onkeydown={(e) => e.key === 'Escape' && (adding = false)}
					/>
				</form>
			{/if}

			{#if addingGroup}
				<form onsubmit={createGroup}>
					<input
						bind:value={newGroupName}
						use:focusOnMount
						placeholder={t('sidebar.groupName')}
						aria-label={t('sidebar.groupName')}
						onblur={() => !newGroupName.trim() && (addingGroup = false)}
						onkeydown={(e) => e.key === 'Escape' && (addingGroup = false)}
					/>
				</form>
			{/if}

			{#if !app.sectionCollapsed('projects')}
				{#each ungrouped as project (project.id)}
					{@render projectRow(project, true)}
				{/each}

				{#if own.length === 0 && !adding}
					<p class="empty">{t('sidebar.noProjects')}</p>
				{/if}
			{/if}
		</div>

		{#each groups as group (group.id)}
			{@const inside = inGroup(group.id)}
			<div class="group folder">
				<!-- Draggable, except while it is being renamed: a draggable ancestor stops
				     the pointer from selecting text in the field it contains. -->
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div
					class="group-head folder-head"
					class:over={overGroup === group.id}
					class:drop-above={overId === group.id && !overBelow}
					class:drop-below={overId === group.id && overBelow}
					class:dragging={draggingGroup === group.id}
					draggable={renamingGroup !== group.id}
					ondragstart={(e) => onGroupDragStart(e, group)}
					ondragend={clearDrag}
					ondragover={(e) => onGroupDragOver(e, group)}
					ondragleave={() => {
						overGroup = null;
						overId = null;
					}}
					ondrop={(e) => onGroupDrop(e, group)}
					ondblclick={() => startRename(group)}
					onkeydown={(e) => {
						if (e.key === 'F2') {
							e.preventDefault();
							startRename(group);
						}
					}}
				>
					{#if renamingGroup === group.id}
						<!-- Renaming is where the colour is chosen too.
						     The dot used to be a button in the row, which made the heading
						     carry four controls — dot, name, Omdøb, Slet — on a line narrow
						     enough that they crowded the name. Both are edits to the same
						     thing, so both belong in the same moment: you open the group to
						     change it, and change what you came to change.

						     `focusout` rather than `blur` closes it, and only when focus has
						     left the form altogether — clicking a swatch is still inside it.
						     The swatches also refuse focus on mousedown, because Safari and
						     Firefox do not focus a button on click, so `relatedTarget` would
						     be null and the form would shut under the pointer. -->
						<form
							class="renaming"
							onsubmit={(e) => renameGroup(e, group)}
							onfocusout={(e) => {
								if (!e.currentTarget.contains(e.relatedTarget)) renamingGroup = null;
							}}
						>
							<input
								bind:value={groupName}
								use:focusOnMount
								aria-label={t('sidebar.groupName')}
								onkeydown={(e) => e.key === 'Escape' && (renamingGroup = null)}
							/>
							<div class="swatches" role="group" aria-label={t('group.colorOf', { name: group.name })}>
								{#each COLORS as color (color.id)}
									<button
										type="button"
										class="swatch"
										class:chosen={(group.color ?? 'graphite') === color.id}
										style="background: {colorVar(color.id)}"
										title={t(color.name)}
										aria-label={t(color.name)}
										aria-pressed={(group.color ?? 'graphite') === color.id}
										onmousedown={(e) => e.preventDefault()}
										onclick={() => app.setGroupColor(group.id, color.id)}
									></button>
								{/each}
							</div>
						</form>
					{:else}
						<!-- The button sits inside the heading, not around it, the same way a
						     project's title does: a group *is* a heading in the sidebar, and
						     wrapping an h2 in a button both invalidates the markup and takes
						     the heading away from a screen reader. The whole heading folds —
						     at this size a lone chevron is a target you miss, and the name is
						     where the eye already is. -->
						<!-- The chevron folds; the name opens the group's own page. Two
						     targets in one heading, because they are two different intentions
						     and the old single one could only ever serve the smaller. -->
						<button
							class="chevron-button"
							onclick={() => app.toggleGroup(group.id)}
							aria-expanded={!group.collapsed}
							aria-label={group.collapsed
								? t('sidebar.unfold', { name: group.name })
								: t('sidebar.fold', { name: group.name })}
						>
							<svg
								class="chevron"
								class:collapsed={group.collapsed}
								viewBox="0 0 24 24"
								aria-hidden="true"
							>
								<path d="M6 9l6 6 6-6" />
							</svg>
						</button>
						<h2 class="fold">
							<!-- Navigation waits two tenths of a second for a second click. A
							     link cannot know a double is coming without pausing, and the
							     alternative was worse than the pause: the first click opened the
							     page while the rename form was appearing, and the colour swatches
							     moved out from under the pointer. Two tenths is under the double
							     click threshold and over the threshold of noticing. -->
							<a
								href="/gruppe/{group.id}"
								onclick={(e) => {
									e.preventDefault();
									clearTimeout(navTimer);
									if (e.detail > 1) return;
									navTimer = setTimeout(() => {
										onnavigate();
										goto(`/gruppe/${group.id}`);
									}, 200);
								}}
							>
								{@render sectionIcon('group')}
								{group.name}
							</a>
						</h2>
						<span class="count">{inside.length}</span>
					{/if}
				</div>

				{#if !group.collapsed}
					{#each inside as project (project.id)}
						{@render projectRow(project, true)}
					{/each}
					{#if !inside.length}
						<p class="empty">{t('sidebar.emptyGroup')}</p>
					{/if}
				{/if}
			</div>
		{/each}

		{#if shared.length}
			<div class="group">
				<div class="group-head">{@render foldHeading('shared', t('sidebar.shared'))}</div>
				{#if !app.sectionCollapsed('shared')}
					{#each shared as project (project.id)}
						{@render projectRow(project, false)}
					{/each}
				{/if}
			</div>
		{/if}

		{#if filters.length}
			<div class="group">
				<div class="group-head">{@render foldHeading('filters', t('sidebar.filters'))}</div>
				{#if !app.sectionCollapsed('filters')}
					{#each filters as filter (filter.id)}
						<a
							href="/filter/{filter.id}"
							class:active={current === `/filter/${filter.id}`}
							onclick={onnavigate}
						>
							<span class="dot" aria-hidden="true"></span>
							{filter.name}
						</a>
					{/each}
				{/if}
			</div>
		{/if}

		{#if labels.length}
			<div class="group">
				<div class="group-head">{@render foldHeading('labels', t('sidebar.labels'))}</div>
				{#if !app.sectionCollapsed('labels')}
					{#each labels.filter((l) => l.task_count > 0) as label (label.id)}
						<a
							href="/etiket/{encodeURIComponent(label.name)}"
							class:active={current === `/etiket/${encodeURIComponent(label.name)}`}
							onclick={onnavigate}
						>
							<span class="dot" aria-hidden="true"></span>
							{label.name}
							<span class="count">{label.task_count}</span>
						</a>
					{/each}
				{/if}
			</div>
		{/if}

		<div class="foot">
			<a
				href="/indstillinger"
				class="settings"
				class:active={current.startsWith('/indstillinger')}
				onclick={onnavigate}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<circle cx="12" cy="12" r="3" />
					<path
						d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 11-4 0v-.09A1.65 1.65 0 008 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06A1.65 1.65 0 004.6 15a1.65 1.65 0 00-1.51-1H3a2 2 0 110-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06A1.65 1.65 0 009 4.6a1.65 1.65 0 001-1.51V3a2 2 0 114 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06A1.65 1.65 0 0019.4 9v0a1.65 1.65 0 001.51 1H21a2 2 0 110 4h-.09a1.65 1.65 0 00-1.51 1z"
					/>
				</svg>
				{t('nav.settings')}
			</a>

			<!-- A quiet indicator, not an alarm: losing the socket for a moment is
			     normal, and the app keeps working without it. -->
			{#if !app.connected}
				<span class="offline" title={t('nav.offlineHint')}>{t('nav.offline')}</span>
			{/if}
			<button class="user" onclick={signOut}>
				<span class="avatar" style="background: {app.user?.avatar_color}">
					{app.user?.name?.[0]?.toUpperCase() ?? '?'}
				</span>
				<span class="user-name">{app.user?.name}</span>
				<span class="signout">{t('nav.signOut')}</span>
			</button>
		</div>
	</div>
	<!-- Also a slider for the keyboard: arrow keys move it, and Home puts it back.
	     A drag handle with no keyboard equivalent is a setting some people simply
	     cannot reach. -->
	<div
		class="resize"
		class:resizing
		role="separator"
		aria-orientation="vertical"
		aria-label={t('nav.sidebarWidth')}
		tabindex="0"
		onpointerdown={startResize}
		onpointermove={onResize}
		onpointerup={endResize}
		onpointercancel={endResize}
		ondblclick={() => sidebar.reset()}
		onkeydown={(e) => {
			if (e.key === 'ArrowLeft') sidebar.setWidth(sidebar.width - 16);
			else if (e.key === 'ArrowRight') sidebar.setWidth(sidebar.width + 16);
			else if (e.key === 'Home') sidebar.reset();
			else return;
			e.preventDefault();
		}}
	></div>
</nav>

<style>
	.sidebar {
		width: var(--sidebar-width);
		flex: none;
		display: flex;
		flex-direction: column;
		gap: var(--s5);
		padding: var(--s4) var(--s3);
		background: var(--surface-sunken);
		border-right: 1px solid var(--line);
		/* Selve bjælken ruller ikke længere — .scroller gør. `hidden` frem for
		   ingenting, fordi den stadig er den yderste kant: løber en række over,
		   skal den klippes her og ikke skubbe kolonnen bredere. */
		overflow: hidden;
		padding-left: max(var(--s3), env(safe-area-inset-left));
		position: relative;
	}

	/* `min-height: 0`, ellers nægter et flex-element at blive lavere end sit
	   indhold, og boksen ville vokse ned gennem bunden af skærmen i stedet for at
	   rulle — samme fælde som `min-width: 0` på kontonavnet længere nede.

	   Ikke `overflow-x: hidden` her: `overflow-y: auto` gør den anden akse til
	   `auto` af sig selv, og en vandret rullebjælke under menuen er en fejl i en
	   række, ikke i boksen. At klippe den ville skjule fejlen; prøven måler begge
	   bokse. */
	.scroller {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: var(--s5);
		/* The sidebar is padded, so the scrollbar sat a pad-width inside the edge and
		   read as floating in the middle. Pull the scroller out to the border and pad
		   the content back, so the bar sits against the edge where a scrollbar
		   belongs — the rows do not move. Thin and dim, so it is present without
		   drawing the eye. */
		margin-right: calc(var(--s3) * -1);
		padding-right: var(--s3);
		scrollbar-width: thin;
		scrollbar-color: var(--line-strong) transparent;
	}

	.scroller::-webkit-scrollbar {
		width: 6px;
	}

	/* No transparent border and no padding-box clip: those centred a 4px bar in a
	   10px track, so the thumb floated three pixels shy of the divider. The bar is
	   the track now — a thin rounded thumb sitting flush against the line on the
	   right, which is where a scrollbar belongs. */
	.scroller::-webkit-scrollbar-thumb {
		background: var(--line-strong);
		border-radius: 999px;
	}

	.scroller::-webkit-scrollbar-track {
		background: transparent;
	}

	/* Wider than it looks: a 1px target is a target you miss. The visible line stays
	   the border; this only widens what the pointer hits.

	   Flush with the inside edge rather than straddling the border. It used to sit
	   at `right: -3px`, and an absolutely positioned child still counts towards its
	   containing block's scrollable overflow — so those three pixels gave the whole
	   sidebar a horizontal scrollbar on every desktop, always, whatever was in it.
	   Seven pixels inside the edge is the same target for anybody aiming at the
	   border, and it is a target that is actually inside the box it belongs to. */
	.resize {
		position: absolute;
		top: 0;
		bottom: 0;
		right: 0;
		width: 7px;
		cursor: col-resize;
		z-index: 10;
	}

	.resize::after {
		content: '';
		position: absolute;
		inset: 0 3px;
		background: var(--accent);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	.resize:hover::after,
	.resize:focus-visible::after,
	.resize.resizing::after {
		opacity: 1;
	}

	.resize:focus-visible {
		outline: none;
	}

	@media (max-width: 820px) {
		/* The drawer has a fixed width and no room to drag against. */
		.resize {
			display: none;
		}
	}

	.brand {
		display: flex;
		align-items: center;
		gap: var(--s2);
		padding: 0 var(--s2);
	}

	.rune {
		font-size: var(--text-xl);
		color: var(--accent);
		line-height: 1;
	}

	.name {
		font-size: var(--text-lg);
		font-weight: 560;
		letter-spacing: -0.02em;
	}

	.views,
	.group {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.navrow {
		position: relative;
		display: flex;
		align-items: center;
	}

	/* Takes no room until it is wanted: it sits in the padding the row already has,
	   so nothing moves when it appears. */
	.grip {
		position: absolute;
		left: 0;
		top: 0;
		bottom: 0;
		width: 14px;
		cursor: grab;
	}

	.grip:active {
		cursor: grabbing;
	}

	.navrow > :global(a) {
		flex: 1;
		min-width: 0;
	}

	.navrow.dragging {
		opacity: 0.4;
	}

	/* A line where it will land, not a box around what it will land on: the first
	   reads as a position and the second as a target. */
	.navrow.over::before {
		content: '';
		position: absolute;
		top: -1px;
		left: var(--s2);
		right: var(--s2);
		height: 2px;
		background: var(--accent);
		border-radius: 1px;
	}

	/* Arket. Samme plads som prikken, så teksten står på linje med resten af runden
	   — mærket skifter, kolonnen gør ikke. */
	.mark {
		width: 13px;
		height: 13px;
		/* Prikken er 6px bred, og et 13px mærke ville skubbe etiketten tre pixels ud
		   af flugt med de andre. Negativ margen på hver side lægger det tilbage. */
		margin: 0 -3.5px;
		color: var(--ink-faint);
		flex: none;
	}

	/* The fixed views carry a colour of their own, the way Things does — the star
	   amber, the Inbox blue — and keep it whether or not they are the active row.
	   So no active-state override here; the colour is the icon's identity. */
	[data-view='inbox'] .mark {
		color: var(--color-blue);
	}
	[data-view='today'] .mark {
		color: var(--color-amber);
	}
	[data-view='upcoming'] .mark {
		color: var(--color-tomato);
	}
	[data-view='delegated'] .mark {
		color: var(--color-violet);
	}
	[data-view='notes'] .mark {
		color: var(--color-teal);
	}
	[data-view='calendar'] .mark {
		color: var(--color-indigo);
	}

	.group-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0 var(--s2) var(--s2);
	}

	/* The Projekter heading holds the only way to add a project or a group, so it
	   must not scroll away with a long list. Pinned to the top of the scroller, on
	   the sidebar's own ground so the rows pass under it. */
	.sticky-head {
		position: sticky;
		top: 0;
		z-index: 3;
		padding-top: var(--s2);
		background: var(--surface-sunken);
	}

	/* Every heading — the system sections and the user's groups — reads the same:
	   a mark, then a bold plainly-cased name. What used to set them apart (one a
	   faint uppercase label, the other a bold name with a coloured dot) is gone. */
	h2 {
		font-size: var(--menu-size);
		font-weight: 600;
		color: var(--ink);
	}

	/* The heading's mark, in the muted ink so the bold name still leads. Sized and
	   nudged like a row's own mark, so the icons line up down the sidebar. */
	.head-icon {
		width: 14px;
		height: 14px;
		flex: none;
		color: var(--ink-muted);
	}

	/* Only the square icon buttons — "+" and the folder — and asked for by class
	   rather than by "a button in a heading". A group's heading also has Omdøb and
	   Slet in it, and they are words: a selector that reached them squeezed both
	   into a 20px box, where they overflowed and were drawn on top of each other.
	   The class is the boundary, so the next text button added here is safe. */
	.group-head > button.icon {
		width: 20px;
		height: 20px;
		display: grid;
		place-items: center;
		border-radius: var(--radius-sm);
		color: var(--ink-faint);
		font-size: var(--text-lg);
		line-height: 1;
		flex: none;
	}

	.group-head > button.icon:hover {
		color: var(--ink);
		background: var(--surface-raised);
	}

	.new-group svg {
		width: 14px;
		height: 14px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.6;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* --- groups ------------------------------------------------------------- */

	.folder {
		margin-top: calc(var(--s3) * -1);
	}

	.folder-head {
		position: relative;
		gap: var(--s1);
		border-radius: var(--radius);
	}

	.folder-head.dragging {
		opacity: 0.4;
	}

	/* The heading lights up as a whole, because the whole heading is the target —
	   unlike a project row, where the target is the gap next to it. Both headings
	   do it: "Projekter" is where a project goes to leave its group. */
	.group-head.over {
		background: var(--surface-raised);
		box-shadow: inset 0 0 0 1px var(--accent);
		border-radius: var(--radius);
	}

	.folder-head::before {
		content: '';
		position: absolute;
		left: var(--s2);
		right: var(--s2);
		height: 2px;
		background: var(--accent);
		opacity: 0;
		pointer-events: none;
	}

	.folder-head.drop-above::before {
		top: -1px;
		opacity: 1;
	}

	.folder-head.drop-below::before {
		bottom: -1px;
		opacity: 1;
	}

	.fold {
		flex: 1;
		min-width: 0;
	}

	/* The chevron on its own, so the name beside it can be a link to the group's
	   page. Sized as a target rather than as a glyph: 11px of arrow is something
	   you miss. */
	/* The chevron sits on top of the group's dot rather than before it. In the flow
	   it pushed the name 22px further in than a project's, so groups read as if
	   they were filed under the projects above them — the opposite of what they
	   are. Out of the flow, a group's name starts exactly where a project's does.
	   The dot is the resting state; the chevron takes over on hover or focus, and
	   stays put while the group is folded, so a folded group still shows the way
	   back open. */
	/* Uden baggrund. Knappen dækkede prikken med et felt i --surface, og på den
	   sænkede bjælke er det en lys firkant: en foldet gruppe stod med en hvid
	   klods ud for navnet permanent, fordi chevronen bliver stående, når gruppen
	   er lukket. Prikken tones ud i stedet for at blive dækket — det er samme
	   skifte, uden noget at male henover den med. */
	/* A group folds from its own chevron, the same one the system sections carry —
	   in the flow before the name now, not an absolute mark that appeared on hover,
	   so a group and a section read identically at rest. The name beside it is still
	   a link to the group's page; the chevron is the second intention. */
	.chevron-button {
		flex: none;
		width: 14px;
		height: 14px;
		display: grid;
		place-items: center;
		color: inherit;
	}

	.chevron-button:hover {
		color: var(--ink);
	}

	.fold a,
	.fold button {
		display: flex;
		align-items: center;
		gap: var(--s1);
		width: 100%;
		min-width: 0;
		font: inherit;
		letter-spacing: inherit;
		text-transform: inherit;
		color: inherit;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.fold a:hover,
	.fold button:hover {
		color: var(--ink);
	}

	.fold a {
		text-decoration: none;
	}

	.chevron {
		width: 11px;
		height: 11px;
		flex: none;
		fill: none;
		stroke: currentColor;
		stroke-width: 2.4;
		stroke-linecap: round;
		stroke-linejoin: round;
		transition: transform var(--fast) var(--ease);
	}

	.chevron.collapsed {
		transform: rotate(-90deg);
	}

	/* Hidden until the heading is hovered, like the section actions on a project
	   page: two buttons permanently beside a label stop it reading as a label. */
	.folder-head .count {
		margin-left: auto;
	}

	/* Without min-width a flex item refuses to shrink below its content, so a long
	   group name pushed the count off the row instead of ending in an ellipsis. */
	.folder-head h2 {
		min-width: 0;
		flex: 1;
	}

	/* No left padding of its own: the row already has the same padding as a project
	   row, and the anchor's would be added on top — which is where the last eight
	   pixels of the old indent were hiding. Vertical padding stays, so the heading
	   keeps its height. */
	.folder-head h2 a {
		display: flex;
		align-items: center;
		gap: var(--s2);
		min-width: 0;
		padding: var(--s2) 0;
	}

	.folder-head h2 a :global(*:last-child),
	.folder-head h2 {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Projects sit at the same left margin whether or not they are in an area —
	   one straight edge down the whole sidebar reads calmer than a staircase, and
	   the bold area heading above already does the grouping the indent used to. */
	.folder > a {
		padding-left: var(--s2);
	}

	.folder > a.sortable::before {
		left: var(--s2);
	}

	.folder > .empty {
		padding-left: var(--s2);
	}

	/* Inside the rename form now, so it needs no padding of its own — the form
	   already sits where the heading did. */
	.renaming {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		width: 100%;
		min-width: 0;
	}

	.swatches {
		display: flex;
		flex-wrap: wrap;
		gap: var(--s2);
	}

	.swatch {
		width: 16px;
		height: 16px;
		border-radius: var(--radius-full);
		flex: none;
		/* A ring in the ground colour, so the chosen one reads as selected without
		   the swatch changing size and shuffling the row as you move along it. */
		box-shadow: 0 0 0 2px var(--surface-sunken);
		transition: box-shadow var(--fast) var(--ease);
	}

	.swatch:hover,
	.swatch:focus-visible {
		box-shadow:
			0 0 0 2px var(--surface-sunken),
			0 0 0 3px var(--line-strong);
		outline: none;
	}

	.swatch.chosen {
		box-shadow:
			0 0 0 2px var(--surface-sunken),
			0 0 0 3px var(--ink);
	}

	.folder-head form {
		flex: 1;
		min-width: 0;
	}

	a {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s2) var(--s2);
		border-radius: var(--radius);
		color: var(--ink-muted);
		text-decoration: none;
		/* Its own size (Settings → Appearance) and medium weight, so the menu items
		   carry more than the muted small text they used to and read at a glance. */
		font-size: var(--menu-size);
		font-weight: 500;
		transition:
			background var(--fast) var(--ease),
			color var(--fast) var(--ease);
	}

	a:hover {
		background: var(--surface);
		color: var(--ink);
	}

	/* An accent-tinted selection, not a half-step of surface: on the sunken dark
	   sidebar --surface-raised was all but invisible, so the active row read as no
	   different from the rest. --accent-sunken carries the accent hue and stands out
	   in both themes. */
	a.active {
		background: var(--accent-sunken);
		color: var(--ink);
		font-weight: 600;
	}

	/* Only your own projects reorder. A shared one sits where its owner put it —
	   sort_order is a column on the project, not a preference per viewer. */
	a.sortable {
		position: relative;
	}

	a.sortable.dragging {
		opacity: 0.4;
	}

	/* A line in the gap rather than a highlighted row: the gap is the target. */
	a.sortable::before {
		content: '';
		position: absolute;
		left: var(--s2);
		right: var(--s2);
		height: 2px;
		background: var(--accent);
		opacity: 0;
		pointer-events: none;
		transition: opacity var(--fast) var(--ease);
	}

	a.sortable.drop-above::before {
		top: -1px;
		opacity: 1;
	}

	a.sortable.drop-below::before {
		bottom: -1px;
		opacity: 1;
	}

	/* A task hovering over a project lights the whole row, where a project hovering
	   between two rows draws a line in the gap. Two different questions — "into
	   this one" and "between these two" — so two different marks. */
	a.receiving {
		background: var(--surface-raised);
		box-shadow: inset 0 0 0 1px var(--accent);
		color: var(--ink);
	}

	.dot {
		width: 6px;
		height: 6px;
		border-radius: var(--radius-full);
		background: var(--line-strong);
		flex: none;
	}

	a.active .dot,
	.dot.today {
		background: var(--accent);
	}

	.count {
		margin-left: auto;
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.empty {
		margin: 0;
		padding: var(--s2);
		font-size: var(--text-sm);
		color: var(--ink-faint);
	}

	form input {
		width: 100%;
		padding: var(--s2);
		background: var(--surface);
		border: 1px solid var(--accent);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
	}

	.foot {
		margin-top: auto;
		padding-top: var(--s4);
		border-top: 1px solid var(--line);
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	.settings svg {
		width: 15px;
		height: 15px;
		flex: none;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.6;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.offline {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding: 0 var(--s2);
	}

	.user {
		display: flex;
		align-items: center;
		gap: var(--s2);
		padding: var(--s2);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		width: 100%;
	}

	.user:hover {
		background: var(--surface);
	}

	.avatar {
		width: 22px;
		height: 22px;
		flex: none;
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		font-size: var(--text-xs);
		font-weight: 560;
		color: #fff;
		flex: none;
	}

	/* `min-width: 0`, or the ellipsis never happens.
	   A flex item's default is `min-width: auto`, which refuses to shrink below its
	   content — so `overflow: hidden` had nothing to hide and the row simply grew
	   wider than the sidebar. And the sidebar has `overflow-y: auto`, which CSS
	   resolves to `auto` on the other axis too, so the result was a horizontal
	   scrollbar under the whole menu on a perfectly ordinary desktop.

	   Not fixed with `overflow-x: hidden` on the sidebar: the resize handle sits at
	   `right: -3px` and would be clipped away. The row shrinking is the fix; the
	   test asserting the sidebar never scrolls sideways is the backstop. */
	.user-name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.signout {
		margin-left: auto;
		flex: none;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	.user:hover .signout {
		opacity: 1;
	}
</style>
