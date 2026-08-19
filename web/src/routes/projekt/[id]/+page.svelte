<script>
	/** One project: its sections, its tasks, and who it is shared with. */
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api, humanMessage } from '$lib/api.js';
	import { app, completedView, projectName } from '$lib/stores.svelte.js';
	import { COLORS, colorVar } from '$lib/colors.js';
	import { eventName, eventDetail } from '$lib/events.js';
	import { TASK, carries, dragged, accept } from '$lib/dnd.js';
	import TaskList from '$lib/components/TaskList.svelte';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import QuickAdd from '$lib/components/QuickAdd.svelte';
	import BoardView from '$lib/components/BoardView.svelte';
	import CalendarView from '$lib/components/CalendarView.svelte';
	import { focusOnMount } from '$lib/focus.js';
	import { t } from '$lib/i18n.svelte.js';

	let project = $state(null);
	/**
	 * loading | ready | denied | failed.
	 *
	 * Four states rather than "is project null", which is what this was: the page
	 * showed "Projektet findes ikke, eller du har ikke adgang til det" before the
	 * first request had even returned, and again if it failed for any reason at
	 * all. A slow load, a dropped connection, a server being restarted underneath
	 * you and a project you genuinely cannot see were one message — and it never
	 * asked again, so the wrong one stayed until somebody reloaded.
	 */
	let status = $state('loading');
	let sections = $state([]);
	let members = $state([]);
	let showShare = $state(false);
	let showLog = $state(false);
	let activity = $state([]);
	let inviteEmail = $state('');
	let inviteRole = $state('editor');
	let inviteLink = $state('');
	let inviteError = $state('');

	let id = $derived($page.params.id);

	$effect(() => {
		load(id);
	});

	// Two ways a note belongs here, and both count.
	//
	// Filed in the project — which is what sharing a note means — and mentioning it
	// by #tag, which is what somebody does while writing without stopping to file
	// anything. Looked up by name for the second, because a #tag is written by hand
	// and a person types the name: the same string the quick-add parser reads out of
	// a task.
	let notes = $state([]);

	$effect(() => {
		const id = project?.id;
		const name = project?.name;
		if (!id || !name) {
			notes = [];
			return;
		}
		Promise.all([
			api.notes({ project: id }).catch(() => ({ notes: [] })),
			api.notesLinking('project', name).catch(() => ({ notes: [] }))
		]).then(([filed, mentioned]) => {
			const seen = new Set();
			notes = [...(filed.notes ?? []), ...(mentioned.notes ?? [])].filter((n) => {
				if (seen.has(n.id)) return false;
				seen.add(n.id);
				return true;
			});
		});
	});

	async function load(projectId) {
		if (!projectId) return;
		status = 'loading';
		try {
			const [p, s] = await Promise.all([
				api.getProject(projectId),
				api.listSections(projectId),
				app.loadTasks({ project_id: projectId })
			]);
			project = p;
			sections = s.sections;
			status = 'ready';
		} catch (e) {
			project = null;
			// 404 is the app's answer for both "no such project" and "not yours" —
			// deliberately, so probing ids teaches nothing. Anything else is the
			// request itself having failed, which is worth saying differently and
			// worth offering to try again.
			status = e?.status === 404 ? 'denied' : 'failed';
		}
	}

	$effect(() => {
		if (showShare && project) {
			api.listMembers(project.id).then((r) => (members = r.members));
		}
	});

	// Loaded when it is opened rather than with the page: it is a record you go
	// looking for, not something every visit should pay for.
	$effect(() => {
		if (showLog && project) {
			api.activity(project.id).then((r) => (activity = r.activity)).catch(() => {});
		}
	});

	function when(iso) {
		const then = new Date(iso);
		const seconds = Math.round((Date.now() - then) / 1000);
		if (seconds < 60) return 'lige nu';
		if (seconds < 3600) return `for ${Math.floor(seconds / 60)} min. siden`;
		if (seconds < 86400) return `for ${Math.floor(seconds / 3600)} timer siden`;
		if (seconds < 604800) return `for ${Math.floor(seconds / 86400)} dage siden`;
		return then.toLocaleDateString('da-DK', { day: 'numeric', month: 'short', year: 'numeric' });
	}

	// The chosen view is kept locally as well as on the project, so switching is
	// instant and only persists once the request lands.
	let view = $state(null);
	let mode = $derived(view ?? project?.view_mode ?? 'list');

	async function setView(next) {
		view = next;
		if (project?.role === 'owner') {
			try {
				await api.updateProject(project.id, { view_mode: next });
			} catch {
				// A view that did not persist is a small thing; it still switched.
			}
		}
	}

	let canEdit = $derived(project?.role === 'owner' || project?.role === 'editor');
	let isOwner = $derived(project?.role === 'owner');

	// Filtered by project as well as by state. The store holds whatever the last
	// view loaded, and a task can now leave this project while you are looking at
	// it — dropped on another project in the sidebar, or moved by somebody else —
	// so "what the page asked for" is not the same as "what is in the store".
	let open = $derived(
		app.tasks.filter((t) => !t.completed && !t.parent_id && t.project_id === project?.id)
	);
	let unsectioned = $derived(open.filter((t) => !t.section_id));

	/**
	 * What has been finished here, newest first.
	 *
	 * In one list at the bottom rather than back among the open ones in their
	 * sections. A closed task has stopped being work and started being a record,
	 * and putting the record back in the middle of the plan makes the plan longer
	 * without making it say more.
	 */
	let done = $derived(
		app.tasks
			.filter((t) => t.completed && !t.parent_id && t.project_id === project?.id)
			.sort((a, b) => (b.completed_at ?? '').localeCompare(a.completed_at ?? ''))
	);

	// Reloads when the setting changes, because whether closed tasks are in the
	// store at all is decided by the request.
	$effect(() => {
		completedView.shown;
		if (project) app.loadTasks({ project_id: project.id });
	});

	let editing = $state(false);
	let choosingColor = $state(false);
	let showMenu = $state(false);

	// Closes on a click anywhere else and on Escape. A menu that only closes by
	// pressing its own button again is one you end up clicking twice to leave.
	$effect(() => {
		if (!showMenu) return;
		const away = (event) => {
			if (!event.target.closest?.('.more')) showMenu = false;
		};
		const escape = (event) => event.key === 'Escape' && (showMenu = false);
		// Capture, so a click that its own handler stops still closes the menu.
		window.addEventListener('click', away, true);
		window.addEventListener('keydown', escape);
		return () => {
			window.removeEventListener('click', away, true);
			window.removeEventListener('keydown', escape);
		};
	});

	/**
	 * The colour is written to the store as well as here: the sidebar reads its own
	 * copy of the project list, and a dot that only changed on this page would be
	 * the same colour in the two places you look at it.
	 */
	async function setColor(color) {
		choosingColor = false;
		project = { ...project, color };
		await app.setProjectColor(project.id, color);
	}

	/** Renames on blur. A no-op when nothing changed, so tabbing through is free. */
	async function rename(input) {
		const name = input.value.trim();
		editing = false;
		if (!name || name === project.name) return;

		try {
			project = await api.updateProject(project.id, { name });
			// The sidebar reads its own copy, so it has to be told.
			await app.refreshProjects();
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function remove() {
		const days = 30;
		const confirmed = confirm(
			t('project.deleteQuestion', { name: projectName(project) }) +
				'\n\n' +
				t('project.deleteRecoverable', { days })
		);
		if (!confirmed) return;

		try {
			await api.deleteProject(project.id);
			await app.refreshProjects();
			goto('/');
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	// --- dropping a task into a section ---------------------------------------------

	/**
	 * The section under the pointer, '' for the unsectioned block at the top.
	 *
	 * Only the task *rows* used to accept a drop, so a section with nothing in it
	 * had nothing to aim at and could not be filled by dragging at all — and a
	 * section you have just made is always empty. The board has always taken a drop
	 * on the column; this is the same idea in the list.
	 */
	let overSection = $state(null);

	/**
	 * Clears the highlight when the drag ends, however it ends.
	 *
	 * `dragleave` and the section's own `ondrop` are not enough. A drop on a *row*
	 * inside a section is handled by TaskList, which stops propagation — so the
	 * section never sees the drop and its frame stayed lit afterwards. A drag
	 * abandoned with Escape has the same shape: no drop at all, and the frame kept
	 * standing until the next one.
	 *
	 * `dragend` fires exactly once per drag, on the source, and bubbles to the
	 * window — so it is the one place that is true for every way a drag can finish.
	 */
	$effect(() => {
		const clear = () => (overSection = null);
		window.addEventListener('dragend', clear);
		return () => window.removeEventListener('dragend', clear);
	});

	function onSectionDragOver(event, sectionId) {
		if (!canEdit || !carries(event, TASK)) return;
		accept(event);
		overSection = sectionId;
	}

	async function onSectionDrop(event, sectionId) {
		event.preventDefault();
		const id = dragged(event, TASK);
		overSection = null;
		if (!id || !canEdit) return;

		const task = app.get(id);
		if (!task || (task.section_id ?? '') === sectionId) return;

		// At the end of the section: dropping on the area rather than between two
		// rows is the coarse gesture, and landing at the top would push whatever is
		// there down.
		const existing = open.filter((t) => (t.section_id ?? '') === sectionId && t.id !== id);
		const after = [...existing].sort((a, b) => a.sort_order - b.sort_order).at(-1);

		const previous = { ...task };
		app.replace(id, { ...task, section_id: sectionId });
		try {
			app.replace(
				id,
				await api.moveTask(id, {
					project_id: project.id,
					section_id: sectionId,
					after_id: after?.id ?? ''
				})
			);
		} catch (e) {
			app.replace(id, previous);
			app.toast(humanMessage(e));
		}
	}

	// --- sections -------------------------------------------------------------------

	let addingSection = $state(false);
	let renamingSection = $state(null);
	let sectionName = $state('');

	/**
	 * A task written straight into a section.
	 *
	 * The quick-add box at the top of the page puts things in the project with no
	 * section, which is right for "capture it now" — but a project with sections has
	 * them because the work belongs somewhere, and dragging every new task down into
	 * the right one is a second gesture for something you already knew when you
	 * typed it.
	 *
	 * It goes through quick add rather than createTask, so a line written here still
	 * parses its dates, priorities and labels. The section is passed rather than
	 * written, so `/Produktion` is not needed when you are already standing in it.
	 */
	let addingIn = $state(null);
	let newInSection = $state('');

	async function addToSection(event, sectionId) {
		event.preventDefault();
		const text = newInSection.trim();
		if (!text) return;
		newInSection = '';
		addingIn = null;
		try {
			const created = await api.quickAdd(text, project.id, sectionId);
			app.upsert(created);
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function addSection(event) {
		event.preventDefault();
		const name = sectionName.trim();
		if (!name) return;
		addingSection = false;
		sectionName = '';
		try {
			sections = [...sections, await api.createSection(project.id, name)];
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function renameSection(event, section) {
		event.preventDefault();
		const name = sectionName.trim();
		renamingSection = null;
		if (!name || name === section.name) return;

		const previous = sections;
		sections = sections.map((s) => (s.id === section.id ? { ...s, name } : s));
		try {
			await api.updateSection(section.id, { name });
		} catch (e) {
			sections = previous;
			app.toast(humanMessage(e));
		}
	}

	async function removeSection(section) {
		const count = open.filter((t) => t.section_id === section.id).length;
		const warning = count
			? `Slet sektionen "${section.name}"? De ${count} opgaver bliver, men mister deres sektion.`
			: `Slet sektionen "${section.name}"?`;
		if (!confirm(warning)) return;

		const previous = sections;
		sections = sections.filter((s) => s.id !== section.id);
		try {
			await api.deleteSection(section.id);
			// The tasks are not deleted with it; they come back unsectioned, and the
			// list has to be re-read to show them in the right place.
			await app.loadTasks({ project_id: project.id });
		} catch (e) {
			sections = previous;
			app.toast(humanMessage(e));
		}
	}

	/**
	 * What each standing is called in the interface.
	 *
	 * The API's words are `owner`, `editor` and `viewer`, and they were being
	 * printed straight into a Danish page. These are the same three words the
	 * invite form already uses, so the list and the form agree.
	 */
	const ROLES = { owner: 'Ejer', editor: 'Kan redigere', viewer: 'Kan kun se' };

	/**
	 * Changes what somebody may do, without removing them.
	 *
	 * Removing and re-inviting was the only way to correct a role, and it
	 * unassigns every task they were responsible for on the way past — so fixing a
	 * dropdown cost somebody their work.
	 */
	async function setMemberRole(member, role) {
		const previous = members;
		members = members.map((m) => (m.user_id === member.user_id ? { ...m, role } : m));
		try {
			await api.setMemberRole(project.id, member.user_id, role);
		} catch (e) {
			members = previous;
			app.toast(humanMessage(e));
		}
	}

	async function invite(event) {
		event.preventDefault();
		inviteError = '';
		inviteLink = '';
		try {
			const result = await api.invite(project.id, inviteEmail, inviteRole);
			inviteEmail = '';
			if (result.link && !result.emailed) {
				// No mail server, or delivery failed. The link is the only way this
				// invite reaches anybody, so it is shown rather than swallowed.
				inviteLink = result.link;
			}
			members = (await api.listMembers(project.id)).members;
		} catch (e) {
			inviteError = humanMessage(e);
		}
	}
</script>

<!-- Wide for a board and a month grid, narrow for a list. Decided here rather than
     in CSS: this was `.view:has(.board)` for as long as the board has existed and
     never once matched, because `.board` is inside a child component and Svelte
     scopes a selector to the component that wrote it. The rule read correctly,
     compiled, and could not fire. Kommende has done it this way all along. -->
<div class="view" class:wide={mode !== 'list'}>
	{#if project}
		<header>
			<!-- A heading first, a field second. Swapping the h1 out for an input
			     outright cost the page its heading — which the sidebar smoke test
			     caught, and which is a real loss: every view here has one, and it is
			     what a screen reader announces on arrival. So the heading stays and
			     becomes editable on click. -->
			{#if editing}
				<input
					class="title"
					use:focusOnMount
					value={project.name}
					aria-label={t('project.name')}
					onblur={(e) => rename(e.currentTarget)}
					onkeydown={(e) => {
						if (e.key === 'Enter') e.currentTarget.blur();
						if (e.key === 'Escape') {
							e.currentTarget.value = project.name;
							editing = false;
						}
					}}
				/>
			{:else}
				<h1 class:renameable={isOwner}>
					{#if isOwner}
						<button onclick={() => (editing = true)} title={t('project.clickToRename')}>
							{projectName(project)}
						</button>
					{:else}
						{projectName(project)}
					{/if}
				</h1>
			{/if}
			<div class="views" role="group" aria-label={t('view.mode')}>
				{#each [['list', t('view.list')], ['board', t('view.board')], ['calendar', t('view.calendar')]] as [value, label]}
					<button
						class:active={mode === value}
						onclick={() => setView(value)}
						aria-pressed={mode === value}>{label}</button
					>
				{/each}
			</div>

			<!-- Everything else lives behind one button.
			     The row had eight controls in it — title, colour, show-done, three view
			     buttons, share, history, delete — and a project name is not a short
			     word. They fitted only on a wide window, and past that the heading was
			     the thing that gave way, which is the one part of the row that has to
			     be readable.

			     What stays out is what you use while working: which view you are in.
			     What moves in is what you do to a project rather than in it, and most
			     of it once ever. -->
			<div class="more">
				<button
					class="more-toggle"
					onclick={() => (showMenu = !showMenu)}
					aria-expanded={showMenu}
					aria-haspopup="menu"
					aria-label={t('project.more')}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<circle cx="5" cy="12" r="1.6" />
						<circle cx="12" cy="12" r="1.6" />
						<circle cx="19" cy="12" r="1.6" />
					</svg>
				</button>

				{#if showMenu}
					<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
					<div class="menu" role="menu">
						<button
							role="menuitem"
							onclick={() => {
								completedView.toggle();
								showMenu = false;
							}}
						>
							{completedView.shown ? t('view.hideDone') : t('view.showDone')}
						</button>

						{#if !project.is_inbox}
							<button
								role="menuitem"
								onclick={() => {
									showShare = !showShare;
									showMenu = false;
								}}
							>
								{project.shared
									? t('project.sharedWith', { n: project.member_count })
									: t('project.share')}
							</button>

							<button
								role="menuitem"
								onclick={() => {
									showLog = !showLog;
									showMenu = false;
								}}
							>
								{t('project.history')}
							</button>
						{/if}

						{#if isOwner && !project.is_inbox}
							<button
								role="menuitem"
								onclick={() => {
									choosingColor = !choosingColor;
									showMenu = false;
								}}
							>
								<span class="swatch-dot" style="background: {colorVar(project.color)}"></span>
								{t('project.color')}
							</button>

							<button
								role="menuitem"
								class="danger"
								onclick={() => {
									showMenu = false;
									remove();
								}}
							>
								{t('project.delete')}
							</button>
						{/if}
					</div>
				{/if}
			</div>
		</header>

		{#if showLog}
			<div class="panel">
				<ul class="log">
					{#each activity as entry (entry.id)}
						<li>
							<!-- Empty when the account has been deleted: the record of what was
							     done outlives whoever did it, so the row stays and the name goes. -->
							<span class="who" class:gone={!entry.user_name}
								>{entry.user_name || t('history.deletedAccount')}</span
							>
							<span class="what">
								{eventName(entry.event)}{#if eventDetail(entry)}
									<span class="detail">{eventDetail(entry)}</span>{/if}
							</span>
							<span class="when">{when(entry.created_at)}</span>
						</li>
					{/each}
				</ul>
				{#if !activity.length}
					<p class="empty">{t('project.noHistory')}</p>
				{/if}
			</div>
		{/if}

		{#if choosingColor}
			<div class="panel swatches" role="group" aria-label={t('project.pickColor')}>
				{#each COLORS as color (color.id)}
					<button
						class="swatch"
						class:chosen={(project.color ?? 'graphite') === color.id}
						style="background: {colorVar(color.id)}"
						title={t(color.name)}
						aria-label={t(color.name)}
						aria-pressed={(project.color ?? 'graphite') === color.id}
						onclick={() => setColor(color.id)}
					></button>
				{/each}
			</div>
		{/if}

		{#if showShare}
			<div class="panel">
				<ul class="members">
					{#each members as member (member.user_id)}
						<li>
							<span class="avatar" style="background: {member.avatar_color}">
								{member.name[0]?.toUpperCase()}
							</span>
							<span class="member-name">{member.name}</span>
							<!-- The owner's standing is not a choice: ownership is transferred,
							     not granted, and the server refuses it. A disabled dropdown
							     would be a control that exists to say no. -->
							{#if isOwner && member.role !== 'owner'}
								<select
									class="role-picker"
									value={member.role}
									aria-label={t('project.roleFor', { name: member.name })}
									onchange={(e) => setMemberRole(member, e.currentTarget.value)}
								>
									<option value="editor">{ROLES.editor}</option>
									<option value="viewer">{ROLES.viewer}</option>
								</select>
							{:else}
								<span class="role">{ROLES[member.role] ?? member.role}</span>
							{/if}
							{#if isOwner && member.role !== 'owner'}
								<button
									class="remove"
									onclick={async () => {
										await api.removeMember(project.id, member.user_id);
										members = (await api.listMembers(project.id)).members;
									}}
									aria-label={t('project.removeMember', { name: member.name })}>×</button
								>
							{/if}
						</li>
					{/each}
				</ul>

				{#if isOwner}
					<form onsubmit={invite}>
						<input
							bind:value={inviteEmail}
							type="email"
							placeholder={t('project.emailAddress')}
							aria-label={t('project.inviteByEmail')}
							required
						/>
						<select bind:value={inviteRole} aria-label={t('project.role')}>
							<option value="editor">{t('project.canEdit')}</option>
							<option value="viewer">{t('project.canView')}</option>
						</select>
						<button type="submit">{t('project.invite')}</button>
					</form>

					{#if inviteLink}
						<p class="link-out">
							{t('project.sendLinkYourself')}
							<code>{inviteLink}</code>
						</p>
					{/if}
					{#if inviteError}
						<p class="error">{inviteError}</p>
					{/if}
				{/if}
			</div>
		{/if}

		{#if canEdit}
			<QuickAdd projectId={project.id} />
		{/if}

		{#if mode === 'board'}
			<BoardView
				{project}
				{sections}
				{canEdit}
				onsectionadded={(section) => (sections = [...sections, section])}
			/>
		{:else if mode === 'calendar'}
			<CalendarView projectId={project.id} />
		{:else}
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<section
				class:over={overSection === ''}
				ondragover={(e) => onSectionDragOver(e, '')}
				ondragleave={() => (overSection = null)}
				ondrop={(e) => onSectionDrop(e, '')}
			>
				<TaskList tasks={unsectioned} projectId={project.id} sectionId="" {canEdit} />
				<!-- Only once there are sections to be "without": on a project with none,
				     the label is a heading for the only list there is, and the empty
				     state below already says the project is empty. -->
				{#if sections.length && !unsectioned.length}
					<p class="empty">{t('project.noSection')}</p>
				{/if}
			</section>

			{#each sections as section (section.id)}
				{@const tasks = open.filter((t) => t.section_id === section.id)}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<section
					class:over={overSection === section.id}
					ondragover={(e) => onSectionDragOver(e, section.id)}
					ondragleave={() => (overSection = null)}
					ondrop={(e) => onSectionDrop(e, section.id)}
				>
					<div class="section-head">
						{#if renamingSection === section.id}
							<form onsubmit={(e) => renameSection(e, section)}>
								<input
									bind:value={sectionName}
									use:focusOnMount
									aria-label={t('view.sectionName')}
									onblur={() => (renamingSection = null)}
									onkeydown={(e) => e.key === 'Escape' && (renamingSection = null)}
								/>
							</form>
						{:else}
							<h2>{section.name}</h2>
							{#if canEdit}
								<button
									class="section-action"
									onclick={() => {
										renamingSection = section.id;
										sectionName = section.name;
									}}>{t('project.rename')}</button
								>
								<button class="section-action remove" onclick={() => removeSection(section)}>
									{t('project.deleteSection')}
								</button>
							{/if}
						{/if}
					</div>
					<TaskList {tasks} projectId={project.id} sectionId={section.id} {canEdit} />
					{#if !tasks.length}
						<p class="empty">{t('view.emptySection')}</p>
					{/if}

					{#if canEdit}
						{#if addingIn === section.id}
							<form class="add-task" onsubmit={(e) => addToSection(e, section.id)}>
								<input
									bind:value={newInSection}
									use:focusOnMount
									placeholder={t('view.addTaskHere')}
									aria-label={t('view.addTaskIn', { name: section.name })}
									onblur={() => !newInSection.trim() && (addingIn = null)}
									onkeydown={(e) => e.key === 'Escape' && (addingIn = null)}
								/>
							</form>
						{:else}
							<button
								class="add-task-open"
								onclick={() => {
									addingIn = section.id;
									newInSection = '';
								}}>{t('view.addTask')}</button
							>
						{/if}
					{/if}
				</section>
			{/each}

			<!-- After the plan, not inside it. -->
			{#if completedView.shown && done.length}
				<section class="done">
					<div class="section-head"><h2>{t('view.done')}</h2></div>
					{#each done as task (task.id)}
						<TaskRow {task} />
					{/each}
				</section>
			{/if}

			{#if canEdit}
				<section class="add-section">
					{#if addingSection}
						<form onsubmit={addSection}>
							<input
								bind:value={sectionName}
								use:focusOnMount
								placeholder={t('view.sectionName')}
								aria-label={t('view.newSection')}
								onblur={() => !sectionName.trim() && (addingSection = false)}
								onkeydown={(e) => e.key === 'Escape' && (addingSection = false)}
							/>
						</form>
					{:else}
						<button
							class="add"
							onclick={() => {
								addingSection = true;
								sectionName = '';
							}}
						>
							{t('view.addSection')}
						</button>
					{/if}
				</section>
			{/if}

			{#if !open.length && !sections.length}
				<p class="clear">
					<span class="rune" aria-hidden="true">ᚹ</span>
					{t('project.nothingHere')}
				</p>
			{/if}
		{/if}

		{#if project.role === 'viewer'}
			<p class="readonly">{t('project.readOnly')}</p>
		{/if}
		<!-- What has been written about this project, which is the payoff for a #tag
		     in a note being the same tag a task carries: nobody files anything here,
		     it turns up because somebody mentioned the project while writing. -->
		{#if notes.length}
			<section class="notes">
				<h2>{t('notes.title')}</h2>
				<ul>
					{#each notes as note (note.id)}
						<li>
							<a href="/noter?note={note.id}">
								<strong>{note.title || t('notes.untitled')}</strong>
								<span>{note.body.slice(0, 90)}</span>
							</a>
						</li>
					{/each}
				</ul>
			</section>
		{/if}
	{:else if status === 'loading'}
		<!-- Deliberately blank, as on first load elsewhere: a spinner for a request
		     that usually resolves in 30ms is a flash of anxiety, not feedback. -->
		<div class="booting"></div>
	{:else if status === 'denied'}
		<p class="clear">{t('project.notFound')}</p>
	{:else}
		<p class="clear">
			<span class="rune" aria-hidden="true">ᚹ</span>
			{t('project.loadFailed')}
			<button class="retry" onclick={() => load(id)}>{t('project.retry')}</button>
		</p>
	{/if}
</div>

<style>
	.notes {
		margin-top: var(--s4);
		padding-top: var(--s3);
		border-top: 1px solid var(--line);
	}

	.notes h2 {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		margin-bottom: var(--s2);
	}

	.notes ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.notes a {
		display: block;
		padding: var(--s2);
		border-radius: var(--radius);
		color: var(--ink-muted);
		text-decoration: none;
	}

	.notes a:hover {
		background: var(--surface);
	}

	.notes strong {
		display: block;
		font-size: var(--text-sm);
		font-weight: 560;
		color: var(--ink);
	}

	.notes span {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	/* Recessed, because it is a record rather than a plan. The rows keep their own
	   completed styling; this is about the section around them. */
	.done {
		opacity: 0.75;
	}

	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	/* A board and a month grid need the room a reading column deliberately
	   withholds. Same allowance Kommende makes. */
	.view.wide {
		max-width: 1400px;
	}

	/* A board and a month grid need the room; a list is easier to read narrow. */

	/* Wraps, and the title keeps a floor.
	 *
	 * Without both, a phone squeezes the heading to its min-content — which with
	 * `overflow-wrap: anywhere` is *one character*, so "Skovvænget" came out as a
	 * vertical column of letters while the buttons ran off the right edge. Four
	 * controls beside a title is more than 390px has, so they belong on the next
	 * line rather than in a race for the same one.
	 */
	header {
		display: flex;
		align-items: center;
		gap: var(--s3);
		flex-wrap: wrap;
		margin-bottom: var(--s5);
	}

	h1 {
		font-size: var(--text-2xl);
		/* Takes the row, and lets the controls keep their own width. With the rest
		   of them behind one button there is room for a real project name. */
		flex: 1 1 auto;
		min-width: 0;
		/* `break-word`, not `anywhere`. `anywhere` breaks inside a word at the first
		   opportunity, so "GarageRisteriet" came out as "GarageRist / eriet" the
		   moment the row was tight — a name split mid-syllable reads as a rendering
		   fault. This only breaks a word that cannot fit on a line of its own. */
		overflow-wrap: break-word;
	}

	/* The heading's button carries no button-ness: it is the heading, and the only
	   hint that it does anything is the underline on hover. */
	h1.renameable button {
		font: inherit;
		letter-spacing: inherit;
		color: inherit;
		text-align: left;
		border-bottom: 1px solid transparent;
	}

	h1.renameable button:hover {
		border-bottom-color: var(--line-strong);
	}

	/* Matches the heading it stands in for, so the swap does not move the page. */
	.title {
		flex: 1 1 8ch;
		min-width: 8ch;
		font-size: var(--text-2xl);
		font-weight: 560;
		letter-spacing: -0.015em;
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius);
		padding: var(--s1) var(--s2);
		margin-left: calc(var(--s2) * -1);
		outline: none;
	}

	.title:hover {
		border-color: var(--line);
	}

	.title:focus {
		background: var(--surface);
		border-color: var(--accent);
	}

	/* --- the overflow menu ------------------------------------------------------ */

	.more {
		position: relative;
		flex: none;
	}

	.more-toggle {
		width: 28px;
		height: 28px;
		display: grid;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-faint);
	}

	.more-toggle:hover,
	.more-toggle[aria-expanded='true'] {
		color: var(--ink);
		background: var(--surface);
	}

	.more-toggle svg {
		width: 18px;
		height: 18px;
		fill: currentColor;
	}

	/* Right-aligned, because the button is at the right end of the row and a menu
	   that opened leftward from there would hang off the page. */
	.menu {
		position: absolute;
		top: calc(100% + var(--s1));
		right: 0;
		z-index: 20;
		min-width: 190px;
		display: flex;
		flex-direction: column;
		padding: var(--s1);
		background: var(--surface-raised);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		box-shadow: var(--shadow-lg);
	}

	.menu button {
		display: flex;
		align-items: center;
		gap: var(--s2);
		width: 100%;
		text-align: left;
		padding: var(--s2) var(--s3);
		border-radius: var(--radius-sm);
		font-size: var(--text-sm);
		color: var(--ink);
		white-space: nowrap;
	}

	.menu button:hover {
		background: var(--surface-sunken);
	}

	.menu button.danger {
		color: var(--danger);
	}

	.menu button.danger:hover {
		background: var(--danger-sunken);
	}

	.swatch-dot {
		width: 10px;
		height: 10px;
		border-radius: var(--radius-full);
		flex: none;
	}

	.remove {
		flex: none;
		width: 30px;
		height: 30px;
		display: grid;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-faint);
		transition:
			color var(--fast) var(--ease),
			background var(--fast) var(--ease);
	}

	.remove:hover {
		color: var(--danger);
		background: var(--danger-sunken);
	}



	.swatches {
		display: flex;
		flex-wrap: wrap;
		gap: var(--s3);
	}

	.swatch {
		width: 22px;
		height: 22px;
		border-radius: var(--radius-full);
		flex: none;
		/* A ring in the panel's own colour, so the selected swatch reads as chosen
		   without changing size and shuffling the row as you move along it. */
		box-shadow: 0 0 0 2px var(--surface);
		transition: box-shadow var(--fast) var(--ease);
	}

	.swatch:hover,
	.swatch:focus-visible {
		box-shadow:
			0 0 0 2px var(--surface),
			0 0 0 3px var(--line-strong);
		outline: none;
	}

	.swatch.chosen {
		box-shadow:
			0 0 0 2px var(--surface),
			0 0 0 3px var(--ink);
	}

	.views {
		display: flex;
		gap: 1px;
		flex: none;
		background: var(--line);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	.views button {
		padding: var(--s1) var(--s3);
		font-size: var(--text-sm);
		background: var(--ground);
		color: var(--ink-muted);
		transition:
			background var(--fast) var(--ease),
			color var(--fast) var(--ease);
	}

	.views button:hover {
		color: var(--ink);
	}

	.views button.active {
		background: var(--surface-raised);
		color: var(--ink);
		font-weight: 500;
	}



	.panel {
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		padding: var(--s4);
		margin-bottom: var(--s5);
		display: flex;
		flex-direction: column;
		gap: var(--s3);
	}

	.members {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	.members li {
		display: flex;
		align-items: center;
		gap: var(--s3);
		font-size: var(--text-sm);
	}

	.avatar {
		width: 22px;
		height: 22px;
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		font-size: var(--text-xs);
		font-weight: 560;
		color: #fff;
		flex: none;
	}

	.member-name {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.role {
		color: var(--ink-faint);
		font-size: var(--text-xs);
	}

	.role-picker {
		flex: none;
		padding: var(--s1) var(--s2);
		background: var(--surface-raised);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		color: var(--ink-muted);
		font-size: var(--text-xs);
		outline: none;
		transition: border-color var(--fast) var(--ease);
	}

	.role-picker:hover,
	.role-picker:focus {
		border-color: var(--accent);
		color: var(--ink);
	}

	.remove {
		color: var(--ink-faint);
		width: 20px;
		height: 20px;
		border-radius: var(--radius-sm);
	}

	.remove:hover {
		color: var(--danger);
		background: var(--danger-sunken);
	}

	form {
		display: flex;
		gap: var(--s2);
		flex-wrap: wrap;
	}

	form input {
		flex: 1;
		min-width: 160px;
	}

	form input,
	form select {
		padding: var(--s2) var(--s3);
		background: var(--surface-raised);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
	}

	form input:focus,
	form select:focus {
		border-color: var(--accent);
	}

	form button {
		padding: var(--s2) var(--s4);
		background: var(--accent);
		color: var(--accent-ink);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		font-weight: 550;
	}

	.log {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		/* Capped and scrolled: a busy project's log is thousands of lines, and a
		   panel that pushes the tasks off the screen is a panel you close again. */
		max-height: 320px;
		overflow-y: auto;
	}

	.log li {
		display: flex;
		align-items: baseline;
		gap: var(--s2);
		padding: var(--s2) 0;
		border-bottom: 1px solid var(--line);
		font-size: var(--text-sm);
	}

	.log li:last-child {
		border-bottom: 0;
	}

	.who {
		font-weight: 500;
		flex: none;
	}

	/* A name that is not a name reads at the weight of the surrounding prose, not
	   at the weight a person's name gets. */
	.who.gone {
		font-weight: 400;
		font-style: italic;
		color: var(--ink-faint);
	}

	.what {
		color: var(--ink-muted);
		flex: 1;
		min-width: 0;
	}

	.detail {
		color: var(--ink);
	}

	.when {
		color: var(--ink-faint);
		font-size: var(--text-xs);
		flex: none;
	}

	.link-out {
		margin: 0;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.link-out code {
		display: block;
		margin-top: var(--s1);
		padding: var(--s2);
		background: var(--surface-sunken);
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		overflow-wrap: anywhere;
		color: var(--ink);
	}

	.error {
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}

	section {
		margin-top: var(--s5);
		border-radius: var(--radius);
	}

	/* The whole section lights up, because the whole section is the target. A line
	   between two rows means "here exactly"; this means "in this one". */
	section.over {
		box-shadow: 0 0 0 1px var(--accent);
		background: var(--surface-sunken);
	}

	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		padding: 0 var(--s2) var(--s2);
		border-bottom: 1px solid var(--line);
		margin-bottom: var(--s2);
	}

	/* A band rather than a rule.
	 *
	 * Small uppercase text over a hairline is a heading you have to look for. A
	 * project with four sections is four groups, and the eye should be able to
	 * count them without reading a word — so the heading gets a ground of its own,
	 * and the rows below sit against the page.
	 *
	 * `--surface`, not a tint of the accent: the colour on this page belongs to the
	 * project, and a section is not a second thing to colour. What is wanted is
	 * separation, and a step in lightness gives that in all five themes without
	 * anybody measuring anything.
	 */
	.section-head {
		display: flex;
		align-items: baseline;
		gap: var(--s3);
		background: var(--surface);
		border-radius: var(--radius);
		padding: var(--s2) var(--s3);
		margin-bottom: var(--s1);
		/* Pulled out to the padding of the rows below, so the band lines up with the
		   text it heads rather than with the rows' own inset. */
		margin-left: calc(var(--s2) * -1);
		margin-right: calc(var(--s2) * -1);
	}

	.section-head h2 {
		border-bottom: 0;
		margin-bottom: 0;
		padding: 0;
		flex: 1;
		min-width: 0;
		color: var(--ink-muted);
	}

	/* Hidden until the section is hovered or something in it has focus: a heading
	   with two buttons permanently beside it stops reading as a heading. */
	.section-action {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding: 0 var(--s1);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	.section-head:hover .section-action,
	.section-action:focus-visible {
		opacity: 1;
	}

	.section-action:hover {
		color: var(--ink);
	}

	.section-action.remove:hover {
		color: var(--danger);
	}

	.section-head form {
		flex: 1;
	}

	.section-head input,
	.add-section input {
		width: 100%;
		padding: var(--s1) var(--s2);
		background: var(--surface-raised);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
	}

	.section-head input:focus,
	.add-section input:focus {
		border-color: var(--accent);
	}

	/* Quiet until wanted. A section that is not being added to should read as a
	   heading and its rows, not as a form. */
	.add-task-open {
		align-self: flex-start;
		padding: var(--s2) 0 var(--s2) var(--s2);
		font-size: var(--text-sm);
		color: var(--ink-faint);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	section:hover .add-task-open,
	.add-task-open:focus-visible {
		opacity: 1;
	}

	.add-task-open:hover {
		color: var(--ink-muted);
	}

	.add-task input {
		width: 100%;
		padding: var(--s2);
		background: transparent;
		border: 0;
		border-bottom: 1px solid var(--accent);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--ink);
		outline: none;
	}

	.add-section {
		margin-top: var(--s5);
	}

	.add-section .add {
		font-size: var(--text-sm);
		color: var(--ink-faint);
		padding: var(--s2);
		transition: color var(--fast) var(--ease);
	}

	.add-section .add:hover {
		color: var(--accent);
	}

	.empty,
	.readonly {
		margin: 0;
		padding: var(--s2);
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}

	.readonly {
		margin-top: var(--s5);
		text-align: center;
	}

	.clear {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--s3);
		padding: var(--s8) var(--s4);
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}

	.rune {
		font-size: var(--text-2xl);
		color: var(--accent);
		opacity: 0.5;
	}

	.booting {
		min-height: 50vh;
	}

	.retry {
		padding: var(--s2) var(--s4);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		transition:
			border-color var(--fast) var(--ease),
			color var(--fast) var(--ease);
	}

	.retry:hover {
		border-color: var(--accent);
		color: var(--ink);
	}
</style>
