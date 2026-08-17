<script>
	/** Import, export and project templates. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { goto } from '$app/navigation';

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
		if (!confirm(`Slet skabelonen "${template.name}"?`)) return;

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
		<h2>Importér fra Todoist</h2>
		<p class="hint">
			Vælg CSV-eksporten af ét projekt. Prioriteterne bliver konverteret, ikke
			kopieret: Todoist skriver 4 for det, grænsefladen kalder P1.
		</p>
	</header>

	<div class="field">
		<label for="todoist">CSV-fil</label>
		<input id="todoist" type="file" accept=".csv,text/csv" onchange={importTodoist} disabled={importing} />
	</div>

	{#if importing}
		<p class="hint">Importerer …</p>
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
					<a class="link" href="/projekt/{result.project_id}">Åbn projektet</a>
				</div>
			{/if}
		</div>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>Papirkurv</h2>
		<p class="hint">
			Slettede projekter, med deres opgaver. De bliver hentet tilbage præcis som
			de var — bortset fra opgaver, du havde slettet hver for sig først; dem var
			der en grund til.
		</p>
	</header>

	{#if loadingTrash}
		<p class="empty">…</p>
	{:else if trashed.length === 0}
		<p class="empty">Papirkurven er tom.</p>
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
					<button class="use" onclick={() => restore(project)}>Hent tilbage</button>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>Eksport</h2>
		<p class="hint">
			Alt, i den rå form. Det er garantien for, at du kan gå igen — en
			eksport, der er blevet pyntet, er en eksport, der har mistet noget.
		</p>
	</header>

	<div class="row">
		<!-- Plain links, not fetch: the browser's own download handling knows what to
		     do with a Content-Disposition, and the session cookie rides along. -->
		<a class="link" href={api.exportAccountURL()} download>Hele kontoen som JSON</a>
	</div>

	<div class="field">
		<label for="export-project">Ét projekt</label>
		<select id="export-project" bind:value={exportProject}>
			<option value="">Vælg et projekt</option>
			{#each app.projects as project (project.id)}
				<option value={project.id}>{project.name}</option>
			{/each}
		</select>
	</div>

	{#if exportProject}
		<div class="row">
			<a class="link" href={api.exportProjectCSVURL(exportProject)} download>CSV</a>
			<a class="link" href={api.exportProjectICSURL(exportProject)} download>Kalenderfil</a>
		</div>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>Skabeloner</h2>
		<p class="hint">
			Et projekt gemt som en form, der kan bruges igen. Datoerne i skabelonen er
			relative, så de bliver regnet ud fra den startdato, du vælger.
		</p>
	</header>

	<form onsubmit={saveTemplate}>
		<div class="field">
			<label for="save-from">Gem et projekt som skabelon</label>
			<select id="save-from" bind:value={saveFrom}>
				<option value="">Vælg et projekt</option>
				{#each projects as project (project.id)}
					<option value={project.id}>{project.name}</option>
				{/each}
			</select>
		</div>

		{#if saveFrom}
			<div class="field">
				<label for="template-name">Navn på skabelonen</label>
				<input
					id="template-name"
					bind:value={templateName}
					placeholder={app.projectById(saveFrom)?.name ?? ''}
				/>
			</div>

			<div class="row">
				<button class="primary" type="submit" disabled={savingTemplate}>Gem som skabelon</button>
			</div>
		{/if}
	</form>

	{#if loadingTemplates}
		<p class="empty">…</p>
	{:else if templates.length === 0}
		<p class="empty">Ingen skabeloner endnu.</p>
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
						Brug
					</button>
					<button class="remove" onclick={() => deleteTemplate(template)}>Slet</button>
				</li>

				{#if usingId === template.id}
					<li class="use-form">
						<form onsubmit={useTemplate}>
							<div class="field">
								<label for="new-name">Navn på det nye projekt</label>
								<input id="new-name" bind:value={newProjectName} placeholder={template.name} />
							</div>

							<div class="field">
								<label for="start">Startdato</label>
								<input id="start" type="date" bind:value={startDate} />
								<p class="hint">Dag nul for skabelonens datoer. Tom betyder i dag.</p>
							</div>

							<div class="row">
								<button class="primary" type="submit">Opret projektet</button>
							</div>
						</form>
					</li>
				{/if}
			{/each}
		</ul>
	{/if}
</section>

<style>
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
