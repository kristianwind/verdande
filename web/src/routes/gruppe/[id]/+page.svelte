<script>
	/**
	 * A group, as a place rather than a heading.
	 *
	 * A heading can carry a name. This carries what the group *is*: the sentence
	 * that says why these projects are one body of work, the documents that belong
	 * to all of them rather than to any one, and the projects themselves with what
	 * is still open in each.
	 *
	 * The count beside a project is open tasks, not all tasks. "12" on a project
	 * you finished last year is a number that means nothing; the question a list of
	 * projects answers is what is left.
	 */
	import { page } from '$app/stores';
	import { api, humanMessage } from '$lib/api.js';
	import { app, projectName } from '$lib/stores.svelte.js';
	import { colorVar } from '$lib/colors.js';
	import { focusOnMount } from '$lib/focus.js';
	import { t } from '$lib/i18n.svelte.js';

	let group = $state(null);
	let projects = $state([]);
	let attachments = $state([]);
	let status = $state('loading');

	let id = $derived($page.params.id);

	$effect(() => {
		load(id);
	});

	async function load(groupId) {
		if (!groupId) return;
		status = 'loading';
		try {
			const r = await api.getProjectGroup(groupId);
			group = r.group;
			projects = r.projects;
			attachments = r.attachments;
			description = r.group.description ?? '';
			status = 'ready';
		} catch (e) {
			group = null;
			// 404 is the answer for both "no such group" and "not yours", so probing
			// ids teaches nothing. Anything else is the request itself having failed,
			// which is worth saying differently and worth offering to retry.
			status = e?.status === 404 ? 'denied' : 'failed';
		}
	}

	// --- the description ---------------------------------------------------------

	let description = $state('');
	let editing = $state(false);
	let saving = $state(false);

	async function saveDescription() {
		const trimmed = description.trim();
		editing = false;
		if (!group || trimmed === (group.description ?? '')) return;

		saving = true;
		const previous = group;
		group = { ...group, description: trimmed };
		try {
			group = await api.updateProjectGroup(group.id, { description: trimmed });
		} catch (e) {
			group = previous;
			description = previous.description ?? '';
			app.toast(humanMessage(e));
		} finally {
			saving = false;
		}
	}

	// --- the documents -----------------------------------------------------------

	let uploading = $state(false);

	async function addFile(event) {
		const file = event.target.files?.[0];
		if (!file) return;
		// Cleared straight away, so choosing the same file twice in a row is two
		// uploads rather than one and a change event that never fires.
		event.target.value = '';

		uploading = true;
		try {
			attachments = [...attachments, await api.uploadGroupAttachment(group.id, file)];
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			uploading = false;
		}
	}

	async function removeFile(attachment) {
		if (!confirm(t('group.removeQuestion', { name: attachment.filename }))) return;

		const previous = attachments;
		attachments = attachments.filter((a) => a.id !== attachment.id);
		try {
			await api.deleteAttachment(attachment.id);
		} catch (e) {
			attachments = previous;
			app.toast(humanMessage(e));
		}
	}

	function size(bytes) {
		if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
		return `${Math.max(1, Math.round(bytes / 1024))} KB`;
	}
</script>

<svelte:head><title>{group?.name ?? t('group.projects')} — verdande</title></svelte:head>

<div class="view">
	{#if status === 'denied'}
		<p class="empty">{t('group.notFound')}</p>
	{:else if status === 'failed'}
		<p class="empty">
			{t('group.failed')}
			<button class="link" onclick={() => load(id)}>{t('group.retry')}</button>
		</p>
	{:else if group}
		<header>
			<span class="dot" style="background: {colorVar(group.color)}" aria-hidden="true"></span>
			<h1>{group.name}</h1>
			<span class="count">{projects.length === 1 ? t('group.projectOne') : t('group.projectMany', { n: projects.length })}</span>
		</header>

		<!-- Click to write, blur to save. The same shape as a task's description,
		     because it is the same act — and a Gem-knap for one field is a button
		     that exists to be pressed once and then be in the way. -->
		<section class="about">
			{#if editing}
				<textarea
					bind:value={description}
					use:focusOnMount
					rows="4"
					aria-label={t('group.about')}
					placeholder={t('group.aboutPlaceholder')}
					onblur={saveDescription}
					onkeydown={(e) => e.key === 'Escape' && (editing = false)}
				></textarea>
			{:else}
				<button class="description" onclick={() => (editing = true)}>
					{#if group.description}
						{group.description}
					{:else}
						<span class="placeholder">{t('group.writeAbout')}</span>
					{/if}
				</button>
			{/if}
			{#if saving}<span class="saved">{t('group.saving')}</span>{/if}
		</section>

		<section class="projects">
			<h2>{t('group.projects')}</h2>
			{#if projects.length}
				<ul>
					{#each projects as project (project.id)}
						<li>
							<a href="/projekt/{project.id}">
								<span class="dot" style="background: {colorVar(project.color)}" aria-hidden="true"
								></span>
								{projectName(project)}
								<!-- Counted by the server in the same request. Open tasks, not all
								     of them: "12" beside a project finished last year is a number
								     that means nothing. -->
								<span class="count">{project.open_tasks}</span>
							</a>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="empty">
					{t('group.noProjects')}
				</p>
			{/if}
		</section>

		<section class="files">
			<h2>{t('group.documents')}</h2>
			<p class="hint">
				{t('group.documentsHint')}
			</p>

			{#if attachments.length}
				<ul class="list">
					{#each attachments as attachment (attachment.id)}
						<li>
							<a class="file" href={api.attachmentURL(attachment.id)} download>
								{attachment.filename}
							</a>
							<span class="secondary">{size(attachment.size)}</span>
							<button class="remove" onclick={() => removeFile(attachment)} aria-label={t('group.removeNamed', { name: attachment.filename })}>
								{t('group.remove')}
							</button>
						</li>
					{/each}
				</ul>
			{/if}

			<label class="add">
				<input type="file" onchange={addFile} disabled={uploading} />
				<span>{uploading ? t('group.uploading') : t('group.addFile')}</span>
			</label>
		</section>
	{/if}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
		display: flex;
		flex-direction: column;
		gap: var(--s5);
	}

	/* Wraps, for the same reason the project header does: a title and a count are
	   more than a phone's width once the title is a real name. */
	header {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: var(--s3);
	}

	h1 {
		font-size: var(--text-2xl);
		min-width: 0;
		overflow-wrap: anywhere;
	}

	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		margin-bottom: var(--s2);
	}

	.dot {
		width: 10px;
		height: 10px;
		border-radius: var(--radius-full);
		flex: none;
		align-self: center;
	}

	.count {
		font-size: var(--text-sm);
		color: var(--ink-faint);
		flex: none;
	}

	.about textarea {
		width: 100%;
		padding: var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font: inherit;
		font-size: var(--text-sm);
		line-height: 1.6;
		resize: vertical;
		outline: none;
	}

	.about textarea:focus {
		border-color: var(--accent);
	}

	.description {
		display: block;
		width: 100%;
		text-align: left;
		padding: var(--s3);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		line-height: 1.6;
		color: var(--ink);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	.description:hover {
		background: var(--surface);
	}

	.placeholder {
		color: var(--ink-faint);
	}

	.projects ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.projects a {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3);
		border-radius: var(--radius);
		color: var(--ink);
		text-decoration: none;
		font-size: var(--text-sm);
		transition: background var(--fast) var(--ease);
	}

	.projects a:hover {
		background: var(--surface);
	}

	.projects .count {
		margin-left: auto;
	}

	.list {
		list-style: none;
		margin: 0 0 var(--s3);
		padding: 0;
		display: flex;
		flex-direction: column;
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	.list li {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3);
		border-bottom: 1px solid var(--line);
	}

	.list li:last-child {
		border-bottom: 0;
	}

	.file {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--text-sm);
		color: var(--ink);
	}

	.secondary {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		flex: none;
	}

	.remove {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		flex: none;
	}

	.remove:hover {
		color: var(--danger);
	}

	.hint {
		margin: 0 0 var(--s3);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		line-height: 1.5;
	}

	/* The input itself is hidden and the label is the control: a browser's own file
	   button cannot be styled, and it says "Vælg fil" in whatever language the
	   browser is in rather than the one the app is in. */
	.add input {
		position: absolute;
		width: 1px;
		height: 1px;
		opacity: 0;
	}

	.add span {
		display: inline-block;
		padding: var(--s2) var(--s4);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		cursor: pointer;
	}

	.add span:hover {
		color: var(--ink);
		border-color: var(--ink-faint);
	}

	.empty {
		font-size: var(--text-sm);
		color: var(--ink-faint);
	}

	.link {
		color: var(--accent);
		text-decoration: underline;
		font-size: inherit;
	}

	.saved {
		font-size: var(--text-sm);
		color: var(--accent);
	}
</style>
