<script>
	/** Import, export and project templates. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { goto } from '$app/navigation';
	import { ago } from '$lib/when.js';
	import { t } from '$lib/i18n.svelte.js';

	// --- backups ---------------------------------------------------------------------

	let backups = $state([]);
	let backupsLoaded = $state(false);
	let backingUp = $state(false);
	let lastBackup = $state('');

	$effect(() => {
		if (!app.user?.is_admin) return;
		api
			.listBackups()
			.then((r) => {
				backups = r.backups;
				backupsLoaded = true;
			})
			.catch((e) => app.toast(humanMessage(e)));
	});

	/**
	 * Takes one now.
	 *
	 * Worth a button of its own rather than trusting the nightly job: waiting until
	 * tonight to find out whether backups work at all is how somebody finds out on
	 * the day they need one. It is also the only way to get a snapshot from *before*
	 * something you are about to do.
	 */
	async function backupNow() {
		backingUp = true;
		lastBackup = '';
		try {
			const made = await api.runBackup();
			backups = [made, ...backups];
			lastBackup = t('data.backupMade', { size: size(made.size_bytes) });
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			backingUp = false;
		}
	}

	function size(bytes) {
		if (!bytes) return t('data.empty');
		const mb = bytes / 1024 / 1024;
		if (mb >= 1) return `${mb.toFixed(1)} MB`;
		return `${Math.max(1, Math.round(bytes / 1024))} KB`;
	}


	// --- Todoist import ------------------------------------------------------------

	let importing = $state(false);
	let result = $state(null);

	async function importTodoist(event) {
		const file = event.target.files?.[0];
		if (!file) return;
		event.target.value = ''; // so the same file can be picked twice after a failure

		importing = true;
		result = null;
		try {
			result = await api.importTodoist(file);
			await app.refreshProjects();
		} catch (e) {
			app.toast(e.fields?.file ?? humanMessage(e));
		} finally {
			importing = false;
		}
	}

	// --- templates --------------------------------------------------------------

	let templates = $state([]);
	let loadingTemplates = $state(true);

	let saveFrom = $state('');
	let templateName = $state('');
	let savingTemplate = $state(false);

	let usingId = $state('');
	let newProjectName = $state('');
	let startDate = $state('');

	$effect(() => {
		api
			.listTemplates()
			.then((r) => (templates = r.templates))
			.catch((e) => app.toast(humanMessage(e)))
			.finally(() => (loadingTemplates = false));
	});

	let projects = $derived(app.projects.filter((p) => !p.is_inbox));

	async function saveTemplate(event) {
		event.preventDefault();
		if (!saveFrom) return;
		savingTemplate = true;
		try {
			const created = await api.saveTemplate({
				project_id: saveFrom,
				name: templateName.trim() || app.projectById(saveFrom)?.name || 'Skabelon',
				description: ''
			});
			templates = [created, ...templates];
			saveFrom = '';
			templateName = '';
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			savingTemplate = false;
		}
	}

	async function useTemplate(event) {
		event.preventDefault();
		if (!usingId) return;
		try {
			const project = await api.useTemplate(usingId, {
				name: newProjectName.trim(),
				start_date: startDate
			});
			await app.refreshProjects();
			usingId = '';
			newProjectName = '';
			startDate = '';
			goto(`/projekt/${project.id}`);
		} catch (e) {
			app.toast(e.fields?.start_date ?? humanMessage(e));
		}
	}

	async function deleteTemplate(template) {
		if (!confirm(t('data.deleteTemplate', { name: template.name }))) return;

		const previous = templates;
		templates = templates.filter((t) => t.id !== template.id);
		try {
			await api.deleteTemplate(template.id);
		} catch (e) {
			templates = previous;
			app.toast(humanMessage(e));
		}
	}

	let exportProject = $state('');

	// --- the trash -------------------------------------------------------------

	let trashed = $state([]);
	let loadingTrash = $state(true);

	$effect(() => {
		api
			.listTrashedProjects()
			.then((r) => (trashed = r.projects))
			.catch((e) => app.toast(humanMessage(e)))
			.finally(() => (loadingTrash = false));
	});

	async function restore(project) {
		const previous = trashed;
		trashed = trashed.filter((p) => p.id !== project.id);
		try {
			await api.restoreProject(project.id);
			await app.refreshProjects();
		} catch (e) {
			trashed = previous;
			app.toast(humanMessage(e));
		}
	}

	/** Whole days left, rounded down — "0 dage tilbage" is the truthful last day. */
	function daysLeft(iso) {
		return Math.max(0, Math.floor((new Date(iso) - Date.now()) / 86400000));
	}
</script>

<section class="panel">
	<header>
		<h2>{t('data.import')}</h2>
		<p class="hint">
			{t('data.importHint')}
		</p>
	</header>

	<div class="field">
		<label for="todoist">{t('data.csvFile')}</label>
		<input id="todoist" type="file" accept=".csv,text/csv" onchange={importTodoist} disabled={importing} />
	</div>

	{#if importing}
		<p class="hint">{t('data.importing')}</p>
	{/if}

	{#if result}
		<div class="result">
			<p class="hint">
				{result.tasks} opgave{result.tasks === 1 ? '' : 'r'}, {result.sections}
				sektion{result.sections === 1 ? '' : 'er'} og {result.comments}
				kommentar{result.comments === 1 ? '' : 'er'} hentet ind.
			</p>
			{#if result.warnings?.length}
				<!-- Reported rather than silently dropped: a partial import that says
				     nothing is discovered weeks later. -->
				<ul class="warnings">
					{#each result.warnings as warning, i (i)}
						<li>{warning}</li>
					{/each}
				</ul>
			{/if}
			{#if result.project_id}
				<div class="row">
					<a class="link" href="/projekt/{result.project_id}">{t('data.openProject')}</a>
				</div>
			{/if}
		</div>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>{t('data.trash')}</h2>
		<p class="hint">
			{t('data.trashHint')}
		</p>
	</header>

	{#if loadingTrash}
		<p class="empty">…</p>
	{:else if trashed.length === 0}
		<p class="empty">{t('data.trashEmpty')}</p>
	{:else}
		<ul class="list">
			{#each trashed as project (project.id)}
				<li>
					<div class="what">
						<span class="title">{project.name}</span>
						<span class="meta">
							{project.task_count} opgave{project.task_count === 1 ? '' : 'r'} ·
							{daysLeft(project.purge_after)} dage tilbage
						</span>
					</div>
					<button class="use" onclick={() => restore(project)}>{t('data.restore')}</button>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<!-- Administrators only, like the error log and for a sharper reason: a backup file
     is a complete copy of the database, so this panel hands out everybody's data. -->
{#if app.user?.is_admin}
	<section class="panel">
		<header>
			<h2>{t('data.backups')}</h2>
			<p class="hint">
				{t('data.backupsHint')}
			</p>
			<p class="hint">
				{t('data.backupsAttachments')}
			</p>
			<!-- Said here rather than only in the documentation: this is the page where
			     somebody downloads the file, and the day the key matters is the day
			     they are restoring it somewhere else and wondering why the mailbox is
			     silent. -->
			<p class="hint">
				{t('data.backupsSecrets')}
			</p>
		</header>

		<div class="row">
			<button class="secondary" onclick={backupNow} disabled={backingUp}>
				{backingUp ? t('data.backingUp') : t('data.backupNow')}
			</button>
			{#if lastBackup}<span class="saved">{lastBackup}</span>{/if}
		</div>

		{#if backups.length}
			<ul class="list">
				{#each backups as backup (backup.id)}
					<li>
						<div class="what">
							<span class="primary-line">
								{ago(backup.started_at)}
								{#if backup.error}<span class="failed">{t('data.backupFailed')}</span>{/if}
							</span>
							<span class="secondary">
								{#if backup.error}
									{backup.error}
								{:else}
									{size(backup.size_bytes)}{#if !backup.present}{' · '}{t('data.swept')}{/if}
								{/if}
							</span>
						</div>
						{#if backup.present && !backup.error}
							<a class="link" href={api.backupURL(backup.id)} download>{t('data.download')}</a>
						{/if}
					</li>
				{/each}
			</ul>
		{:else if backupsLoaded}
			<p class="empty">{t('data.noBackups')}</p>
		{/if}

		<p class="hint">
			{t('data.restoreHint')}
		</p>
	</section>
{/if}

<section class="panel">
	<header>
		<h2>{t('data.export')}</h2>
		<p class="hint">
			{t('data.exportHint')}
		</p>
	</header>

	<div class="row">
		<!-- Plain links, not fetch: the browser's own download handling knows what to
		     do with a Content-Disposition, and the session cookie rides along. -->
		<a class="link" href={api.exportAccountURL()} download>{t('data.exportAccount')}</a>
	</div>

	<div class="field">
		<label for="export-project">{t('data.exportOne')}</label>
		<select id="export-project" bind:value={exportProject}>
			<option value="">{t('data.pickProject')}</option>
			{#each app.projects as project (project.id)}
				<option value={project.id}>{project.name}</option>
			{/each}
		</select>
	</div>

	{#if exportProject}
		<div class="row">
			<a class="link" href={api.exportProjectCSVURL(exportProject)} download>{t('data.csv')}</a>
			<a class="link" href={api.exportProjectICSURL(exportProject)} download>{t('data.ics')}</a>
		</div>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>{t('data.templates')}</h2>
		<p class="hint">
			{t('data.templatesHint')}
		</p>
	</header>

	<form onsubmit={saveTemplate}>
		<div class="field">
			<label for="save-from">{t('data.saveAsTemplate')}</label>
			<select id="save-from" bind:value={saveFrom}>
				<option value="">{t('data.pickProject')}</option>
				{#each projects as project (project.id)}
					<option value={project.id}>{project.name}</option>
				{/each}
			</select>
		</div>

		{#if saveFrom}
			<div class="field">
				<label for="template-name">{t('data.templateName')}</label>
				<input
					id="template-name"
					bind:value={templateName}
					placeholder={app.projectById(saveFrom)?.name ?? ''}
				/>
			</div>

			<div class="row">
				<button class="primary" type="submit" disabled={savingTemplate}>{t('data.saveTemplate')}</button>
			</div>
		{/if}
	</form>

	{#if loadingTemplates}
		<p class="empty">…</p>
	{:else if templates.length === 0}
		<p class="empty">{t('data.noTemplates')}</p>
	{:else}
		<ul class="list">
			{#each templates as template (template.id)}
				<li>
					<div class="what">
						<span class="title">{template.name}</span>
						<span class="meta">
							{template.task_count} opgave{template.task_count === 1 ? '' : 'r'}
						</span>
					</div>
					<button class="use" onclick={() => (usingId = usingId === template.id ? '' : template.id)}>
						{t('data.use')}
					</button>
					<button class="remove" onclick={() => deleteTemplate(template)}>{t('data.delete')}</button>
				</li>

				{#if usingId === template.id}
					<li class="use-form">
						<form onsubmit={useTemplate}>
							<div class="field">
								<label for="new-name">{t('data.newProjectName')}</label>
								<input id="new-name" bind:value={newProjectName} placeholder={template.name} />
							</div>

							<div class="field">
								<label for="start">{t('data.startDate')}</label>
								<input id="start" type="date" bind:value={startDate} />
								<p class="hint">{t('data.startDateHint')}</p>
							</div>

							<div class="row">
								<button class="primary" type="submit">{t('data.createProject')}</button>
							</div>
						</form>
					</li>
				{/if}
			{/each}
		</ul>
	{/if}
</section>

<style>
	/* The backup list, which is a list of rows with a right-hand action — the same
	   shape the settings pages use elsewhere, said once here because this is the
	   only panel on the page that has one. */
	.what {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.primary-line {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
	}

	.secondary {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		overflow-wrap: anywhere;
	}

	.failed {
		font-size: var(--text-xs);
		font-weight: 560;
		color: var(--danger);
		border: 1px solid var(--danger);
		border-radius: var(--radius-sm);
		padding: 0 var(--s1);
	}

	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.result {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		padding: var(--s4);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
	}

	.warnings {
		margin: 0;
		padding-left: var(--s5);
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	.what {
		display: flex;
		flex-direction: column;
		gap: 2px;
		flex: 1;
		min-width: 0;
	}

	.title {
		font-size: var(--text-sm);
	}

	.meta {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.use,
	.remove {
		flex: none;
		font-size: var(--text-xs);
		padding: var(--s1) var(--s2);
		border-radius: var(--radius-sm);
		color: var(--ink-faint);
		transition: color var(--fast) var(--ease);
	}

	.use:hover {
		color: var(--accent);
	}

	.remove:hover {
		color: var(--danger);
		background: var(--danger-sunken);
	}

	.use-form {
		background: var(--surface-sunken);
	}

	.use-form form {
		width: 100%;
	}

	.link {
		font-size: var(--text-sm);
		color: var(--accent);
		text-decoration: none;
	}

	.link:hover {
		text-decoration: underline;
	}
</style>
