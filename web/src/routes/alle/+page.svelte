<script>
	/**
	 * Alle opgaver: everything still open, in one place, in groups.
	 *
	 * By default the groups are the sidebar's own — the Inbox, each project group
	 * with its projects under it, the loose projects, what is shared with you — in
	 * the order the sidebar has them. That is the map somebody already carries in
	 * their head, so the list reads as "the sidebar, opened up" rather than as a new
	 * arrangement to learn. Priority and date are the two other questions a list of
	 * everything gets asked, and they are one press away.
	 *
	 * The server sends the tasks flat and the grouping happens here: the client
	 * already holds the groups and projects with their names, colours and order,
	 * and a second copy of that order on the server is one that drifts.
	 */
	import { api } from '$lib/api.js';
	import { app, projectName } from '$lib/stores.svelte.js';
	import { colorVar } from '$lib/colors.js';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import { t } from '$lib/i18n.svelte.js';

	const MODES = ['group', 'priority', 'date'];
	const MODE_KEY = 'verdande:all-tasks-by';
	const FOLD_KEY = 'verdande:all-tasks-folded';

	let loaded = $state(false);
	let truncated = $state(false);

	let mode = $state(read(MODE_KEY, 'group', (v) => MODES.includes(v)));
	let folded = $state(new Set(read(FOLD_KEY, [], Array.isArray)));

	/** localStorage, but never trusted: private windows throw and old values lie. */
	function read(key, fallback, valid) {
		try {
			const raw = localStorage.getItem(key);
			if (raw === null) return fallback;
			const value = key === FOLD_KEY ? JSON.parse(raw) : raw;
			return valid(value) ? value : fallback;
		} catch {
			return fallback;
		}
	}

	function write(key, value) {
		try {
			localStorage.setItem(key, value);
		} catch {
			// Private browsing; the choice simply will not persist.
		}
	}

	function setMode(value) {
		mode = value;
		write(MODE_KEY, value);
	}

	function toggle(key) {
		const next = new Set(folded);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		folded = next;
		write(FOLD_KEY, JSON.stringify([...next]));
	}

	$effect(() => {
		api.allTasks().then((data) => {
			app.tasks = data.tasks;
			truncated = data.truncated;
			loaded = true;
		});
	});

	// Read back out of the store, so a task closed from the drawer leaves at once,
	// and one moved to another project lands under its new heading straight away:
	// which heading a task belongs to is decided here, not by the response.
	let open = $derived(app.tasks.filter((task) => !task.completed && !task.parent_id));

	// --- grouped by where the task lives ---------------------------------------------

	const byOrder = (a, b) => a.sort_order - b.sort_order;

	/**
	 * Sections, each a list of projects with their open tasks. Projects with nothing
	 * open are left out — this is a list of what is left, and a heading with nothing
	 * under it is a line to read past — and so is a section whose projects are all
	 * empty.
	 */
	let byPlace = $derived.by(() => {
		const tasksIn = new Map();
		for (const task of open) {
			if (!tasksIn.has(task.project_id)) tasksIn.set(task.project_id, []);
			tasksIn.get(task.project_id).push(task);
		}

		const withTasks = (projects) =>
			projects
				.filter((p) => tasksIn.has(p.id))
				.map((p) => ({ project: p, tasks: tasksIn.get(p.id) }));

		const groups = [...app.groups].sort(byOrder);
		const groupIds = new Set(groups.map((g) => g.id));
		const own = app.projects.filter((p) => !p.is_inbox && p.role === 'owner').sort(byOrder);
		const inbox = app.projects.filter((p) => p.is_inbox);

		const sections = [
			{ key: 'inbox', name: t('nav.inbox'), color: '', projects: withTasks(inbox), flat: true },
			...groups.map((g) => ({
				key: `group:${g.id}`,
				name: g.name,
				color: g.color,
				projects: withTasks(own.filter((p) => p.group_id === g.id)),
				flat: false
			})),
			{
				key: 'ungrouped',
				name: t('all.ungrouped'),
				color: '',
				flat: false,
				// Filed by whether the group is one we have, as the sidebar does: a
				// project naming a group deleted in another tab would otherwise be
				// under a heading that is not drawn, which is nowhere.
				projects: withTasks(own.filter((p) => !p.group_id || !groupIds.has(p.group_id)))
			},
			{
				key: 'shared',
				name: t('sidebar.shared'),
				color: '',
				flat: false,
				projects: withTasks(app.projects.filter((p) => !p.is_inbox && p.role !== 'owner'))
			}
		];

		// A task in a project this client does not know — one shared a moment ago,
		// before the project list caught up — still has to be somewhere. Dropping it
		// would make work vanish from the one view that promises all of it.
		const known = new Set(app.projects.map((p) => p.id));
		const stray = open.filter((task) => !known.has(task.project_id));
		if (stray.length) {
			sections.push({
				key: 'other',
				name: t('all.other'),
				color: '',
				projects: [{ project: null, tasks: stray }],
				flat: true
			});
		}

		return sections
			.map((s) => ({ ...s, count: s.projects.reduce((n, p) => n + p.tasks.length, 0) }))
			.filter((s) => s.count > 0);
	});

	// --- grouped by priority -----------------------------------------------------------

	let byPriority = $derived(
		[1, 2, 3, 4]
			.map((p) => ({
				key: `p${p}`,
				name: p === 4 ? t('task.noPriority') : t('task.priority', { n: p }),
				priority: p,
				tasks: open.filter((task) => (task.priority || 4) === p)
			}))
			.filter((s) => s.tasks.length)
	);

	// --- grouped by when ------------------------------------------------------------------

	/** A local calendar day as "YYYY-MM-DD", the shape `due_date` comes in. */
	function dayString(date) {
		const pad = (n) => String(n).padStart(2, '0');
		return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
	}

	let byDate = $derived.by(() => {
		const now = new Date();
		const today = dayString(now);
		const tomorrow = dayString(new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1));
		const week = dayString(new Date(now.getFullYear(), now.getMonth(), now.getDate() + 7));

		const buckets = [
			{ key: 'overdue', name: t('view.overdue'), test: (d) => d && d < today },
			{ key: 'today', name: t('task.today'), test: (d) => d === today },
			{ key: 'tomorrow', name: t('task.tomorrow'), test: (d) => d === tomorrow },
			{ key: 'week', name: t('all.nextSevenDays'), test: (d) => d && d > tomorrow && d <= week },
			{ key: 'later', name: t('all.later'), test: (d) => d && d > week },
			{ key: 'nodate', name: t('all.noDate'), test: (d) => !d }
		];
		return buckets
			.map((b) => ({
				key: b.key,
				name: b.name,
				priority: 0,
				tasks: open.filter((task) => b.test(task.due_date))
			}))
			.filter((b) => b.tasks.length);
	});

	/** For the priority and date views, where the rows come from everywhere. */
	function where(task) {
		return projectName(app.projectById(task.project_id));
	}
</script>

<svelte:head><title>{t('view.all')} — {app.productSlug}</title></svelte:head>

<div class="view">
	<header>
		<h1>{t('view.all')}</h1>
		{#if loaded}<span class="total">{t('all.count', { n: open.length })}</span>{/if}
		<div class="views" role="group" aria-label={t('all.groupBy')}>
			{#each [['group', t('all.byGroup')], ['priority', t('all.byPriority')], ['date', t('all.byDate')]] as [value, label]}
				<button
					class:active={mode === value}
					aria-pressed={mode === value}
					onclick={() => setMode(value)}>{label}</button
				>
			{/each}
		</div>
	</header>

	{#if truncated}
		<!-- Said, not hidden: a list that quietly stops is a list that lies about
		     what is left. -->
		<p class="note">{t('all.truncated')}</p>
	{/if}

	{#snippet heading(key, count, content)}
		<h2>
			<button
				class="fold"
				aria-expanded={!folded.has(key)}
				onclick={() => toggle(key)}
				title={folded.has(key) ? t('all.expand') : t('all.collapse')}
			>
				<svg class="chevron" class:closed={folded.has(key)} viewBox="0 0 16 16" aria-hidden="true">
					<path
						d="M4 6l4 4 4-4"
						fill="none"
						stroke="currentColor"
						stroke-width="1.6"
						stroke-linecap="round"
						stroke-linejoin="round"
					/>
				</svg>
				{@render content()}
			</button>
			<span class="count">{count}</span>
		</h2>
	{/snippet}

	{#if mode === 'group'}
		{#each byPlace as section (section.key)}
			<section data-bucket={section.key}>
				{#snippet label()}
					{#if section.color}
						<span class="dot" style="background: {colorVar(section.color)}" aria-hidden="true"
						></span>
					{/if}
					<span class="name">{section.name}</span>
				{/snippet}
				{@render heading(section.key, section.count, label)}

				{#if !folded.has(section.key)}
					{#each section.projects as entry (entry.project?.id ?? 'stray')}
						{#if !section.flat}
							<a class="project" href="/projekt/{entry.project.id}">
								<span
									class="dot small"
									style="background: {colorVar(entry.project.color)}"
									aria-hidden="true"
								></span>
								{projectName(entry.project)}
								<span class="count">{entry.tasks.length}</span>
							</a>
						{/if}
						<div class:nested={!section.flat}>
							{#each entry.tasks as task (task.id)}
								<TaskRow {task} />
							{/each}
						</div>
					{/each}
				{/if}
			</section>
		{/each}
	{:else}
		{#each mode === 'priority' ? byPriority : byDate as bucket (bucket.key)}
			<section data-bucket={bucket.key}>
				{#snippet label()}
					{#if bucket.priority}
						<span class="flag" data-priority={bucket.priority} aria-hidden="true"></span>
					{/if}
					<span class="name">{bucket.name}</span>
				{/snippet}
				{@render heading(`${mode}:${bucket.key}`, bucket.tasks.length, label)}

				{#if !folded.has(`${mode}:${bucket.key}`)}
					{#each bucket.tasks as task (task.id)}
						<!-- Outside its project a row has to say where it lives: "ring til
						     Jens" under Prioritet 1 means nothing until it says which of
						     five projects. -->
						<TaskRow {task} place={where(task)} />
					{/each}
				{/if}
			</section>
		{/each}
	{/if}

	{#if loaded && !open.length}
		<p class="clear">
			<span class="rune" aria-hidden="true">ᚨ</span>
			{t('all.empty')}
		</p>
	{/if}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	/* Wraps: a title, a count and three buttons are more than a phone's width. */
	header {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: var(--s3);
		margin-bottom: var(--s5);
	}

	h1 {
		font-size: var(--text-2xl);
	}

	.total {
		font-size: var(--text-sm);
		color: var(--ink-faint);
	}

	.views {
		display: flex;
		gap: 1px;
		flex: none;
		margin-left: auto;
		align-self: center;
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

	.note {
		font-size: var(--text-sm);
		color: var(--ink-muted);
		padding: var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		margin-bottom: var(--s4);
	}

	section {
		margin-top: var(--s5);
	}

	h2 {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
		font-weight: 560;
		color: var(--ink-muted);
		padding: 0 var(--s2) var(--s2) 0;
		border-bottom: 1px solid var(--line);
		margin-bottom: var(--s2);
	}

	.fold {
		display: flex;
		align-items: center;
		gap: var(--s2);
		min-width: 0;
		flex: 1;
		text-align: left;
		font: inherit;
		color: inherit;
		padding: var(--s1) 0;
	}

	.fold:hover {
		color: var(--ink);
	}

	.name {
		min-width: 0;
		overflow-wrap: anywhere;
	}

	.chevron {
		width: 14px;
		height: 14px;
		flex: none;
		color: var(--ink-faint);
		transition: transform var(--fast) var(--ease);
	}

	.chevron.closed {
		transform: rotate(-90deg);
	}

	.count {
		margin-left: auto;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		font-weight: 400;
		flex: none;
	}

	.dot {
		width: 10px;
		height: 10px;
		border-radius: var(--radius-full);
		flex: none;
	}

	.dot.small {
		width: 8px;
		height: 8px;
	}

	/* The project is a sub-heading, and a link: the place to go when the list
	   here is not enough. */
	.project {
		display: flex;
		align-items: center;
		gap: var(--s2);
		margin-top: var(--s3);
		padding: var(--s1) var(--s2);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--ink);
		text-decoration: none;
	}

	.project:hover {
		background: var(--surface);
	}

	.nested {
		padding-left: var(--s3);
	}

	.flag {
		width: 10px;
		height: 10px;
		border-radius: 2px;
		flex: none;
		background: var(--ink-faint);
	}

	.flag[data-priority='1'] {
		background: var(--color-tomato);
	}

	.flag[data-priority='2'] {
		background: var(--color-amber);
	}

	.flag[data-priority='3'] {
		background: var(--color-blue);
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
