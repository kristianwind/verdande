<script>
	/** One project: its sections, its tasks, and who it is shared with. */
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskList from '$lib/components/TaskList.svelte';
	import QuickAdd from '$lib/components/QuickAdd.svelte';
	import BoardView from '$lib/components/BoardView.svelte';
	import CalendarView from '$lib/components/CalendarView.svelte';

	let project = $state(null);
	let sections = $state([]);
	let members = $state([]);
	let showShare = $state(false);
	let inviteEmail = $state('');
	let inviteRole = $state('editor');
	let inviteLink = $state('');
	let inviteError = $state('');

	let id = $derived($page.params.id);

	$effect(() => {
		const projectId = id;
		if (!projectId) return;

		Promise.all([
			api.getProject(projectId),
			api.listSections(projectId),
			app.loadTasks({ project_id: projectId })
		])
			.then(([p, s]) => {
				project = p;
				sections = s.sections;
			})
			.catch(() => {
				project = null;
			});
	});

	$effect(() => {
		if (showShare && project) {
			api.listMembers(project.id).then((r) => (members = r.members));
		}
	});

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

	let open = $derived(app.tasks.filter((t) => !t.completed && !t.parent_id));
	let unsectioned = $derived(open.filter((t) => !t.section_id));

	let editing = $state(false);

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
			`Slet "${project.name}" med alle dets opgaver?\n\n` +
				`Det havner i papirkurven og kan hentes tilbage under ` +
				`Indstillinger → Data og skabeloner i ${days} dage.`
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

<div class="view">
	{#if project}
		<header>
			<!-- A heading first, a field second. Swapping the h1 out for an input
			     outright cost the page its heading — which the sidebar smoke test
			     caught, and which is a real loss: every view here has one, and it is
			     what a screen reader announces on arrival. So the heading stays and
			     becomes editable on click. -->
			{#if editing}
				<!-- svelte-ignore a11y_autofocus -->
				<input
					class="title"
					autofocus
					value={project.name}
					aria-label="Projektets navn"
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
						<button onclick={() => (editing = true)} title="Klik for at omdøbe">
							{project.name}
						</button>
					{:else}
						{project.name}
					{/if}
				</h1>
			{/if}
			<div class="views" role="group" aria-label="Visning">
				{#each [['list', 'Liste'], ['board', 'Board'], ['calendar', 'Kalender']] as [value, label]}
					<button
						class:active={mode === value}
						onclick={() => setView(value)}
						aria-pressed={mode === value}>{label}</button
					>
				{/each}
			</div>

			{#if !project.is_inbox}
				<button class="share" onclick={() => (showShare = !showShare)}>
					{project.shared ? `Delt · ${project.member_count}` : 'Del'}
				</button>
			{/if}

			{#if isOwner && !project.is_inbox}
				<button class="remove" onclick={remove} aria-label="Slet projektet">
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M4 7h16M9 7V5a1 1 0 011-1h4a1 1 0 011 1v2M6 7l1 13h10l1-13" />
					</svg>
				</button>
			{/if}
		</header>

		{#if showShare}
			<div class="panel">
				<ul class="members">
					{#each members as member (member.user_id)}
						<li>
							<span class="avatar" style="background: {member.avatar_color}">
								{member.name[0]?.toUpperCase()}
							</span>
							<span class="member-name">{member.name}</span>
							<span class="role">{member.role}</span>
							{#if isOwner && member.role !== 'owner'}
								<button
									class="remove"
									onclick={async () => {
										await api.removeMember(project.id, member.user_id);
										members = (await api.listMembers(project.id)).members;
									}}
									aria-label="Fjern {member.name}">×</button
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
							placeholder="e-mailadresse"
							aria-label="Inviter via e-mail"
							required
						/>
						<select bind:value={inviteRole} aria-label="Rolle">
							<option value="editor">Kan redigere</option>
							<option value="viewer">Kan kun se</option>
						</select>
						<button type="submit">Inviter</button>
					</form>

					{#if inviteLink}
						<p class="link-out">
							Ingen mailserver sat op — send selv linket:
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
			<BoardView {project} {sections} {canEdit} />
		{:else if mode === 'calendar'}
			<CalendarView />
		{:else}
			<section>
				<TaskList tasks={unsectioned} projectId={project.id} sectionId="" {canEdit} />
			</section>

			{#each sections as section (section.id)}
				{@const tasks = open.filter((t) => t.section_id === section.id)}
				<section>
					<h2>{section.name}</h2>
					<TaskList {tasks} projectId={project.id} sectionId={section.id} {canEdit} />
					{#if !tasks.length}
						<p class="empty">Tom</p>
					{/if}
				</section>
			{/each}

			{#if !open.length}
				<p class="clear">
					<span class="rune" aria-hidden="true">ᚹ</span>
					Ingenting her endnu.
				</p>
			{/if}
		{/if}

		{#if project.role === 'viewer'}
			<p class="readonly">Du kan se dette projekt, men ikke ændre det.</p>
		{/if}
	{:else}
		<p class="clear">Projektet findes ikke, eller du har ikke adgang til det.</p>
	{/if}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	/* A board and a month grid need the room; a list is easier to read narrow. */
	.view:has(.board),
	.view:has(.calendar) {
		max-width: 1400px;
	}

	header {
		display: flex;
		align-items: center;
		gap: var(--s4);
		margin-bottom: var(--s5);
	}

	h1 {
		font-size: var(--text-2xl);
		flex: 1;
		min-width: 0;
		overflow-wrap: anywhere;
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
		flex: 1;
		min-width: 0;
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

	.remove svg {
		width: 16px;
		height: 16px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.6;
		stroke-linecap: round;
		stroke-linejoin: round;
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

	.share {
		flex: none;
		font-size: var(--text-sm);
		color: var(--ink-muted);
		padding: var(--s1) var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius-full);
		transition: border-color var(--fast) var(--ease);
	}

	.share:hover {
		border-color: var(--line-strong);
		color: var(--ink);
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
</style>
