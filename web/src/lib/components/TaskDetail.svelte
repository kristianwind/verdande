<script>
	/**
	 * One task, opened.
	 *
	 * A drawer rather than a page: the list you came from stays visible behind it,
	 * so ticking a sub-task off and closing lands you back where you were rather
	 * than at the top of a re-fetched list.
	 *
	 * Text fields save on blur, not on every keystroke. A PATCH per character would
	 * be forty requests for one sentence, and the last one to arrive is not
	 * necessarily the last one sent.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app, projectName } from '$lib/stores.svelte.js';
	import { focusOnMount } from '$lib/focus.js';
	import { t } from '$lib/i18n.svelte.js';

	let { task, onclose } = $props();

	let content = $state('');
	let description = $state('');
	let priority = $state(4);
	let dueDate = $state('');
	let labels = $state('');
	let projectId = $state('');
	let recurrence = $state('');
	let assignee = $state('');

	/** Who the task can be given to. Only ever more than one on a shared project. */
	let members = $state([]);

	// The rules worth a menu entry. Anything more specific is typed into the
	// title, where the parser understands far more than a list ever could.
	const REPEATS = [
		{ rule: '', label: 'Aldrig' },
		{ rule: 'FREQ=DAILY', label: 'Hver dag' },
		{ rule: 'FREQ=WEEKLY', label: 'Hver uge' },
		{ rule: 'FREQ=WEEKLY;INTERVAL=2', label: 'Hver anden uge' },
		{ rule: 'FREQ=MONTHLY', label: t('detail.monthly') },
		{ rule: 'FREQ=YEARLY', label: t('detail.yearly') }
	];

	let subtasks = $state([]);
	let comments = $state([]);
	let attachments = $state([]);
	let notes = $state([]);
	let reminders = $state([]);

	let newSubtask = $state('');
	let newComment = $state('');
	let newReminder = $state('');
	let posting = $state(false);
	let uploading = $state(false);

	// Re-seeded whenever a different task is opened. Keyed on the id rather than on
	// the object, so a websocket update to the same task does not throw away what
	// somebody is typing.
	let seededId = $state(null);

	$effect(() => {
		if (!task || seededId === task.id) return;
		seededId = task.id;

		content = task.content;
		description = task.description ?? '';
		priority = task.priority;
		dueDate = task.due_date ?? '';
		labels = (task.labels ?? []).join(', ');
		projectId = task.project_id;
		recurrence = task.recurrence_rule ?? '';
		assignee = task.assignee_id ?? '';

		members = [];
		api.listMembers(task.project_id)
			.then((r) => r.members.length > 1 && (members = r.members))
			.catch(() => {
				// A project you are not the owner of may refuse the member list.
				// Then there is nobody to choose from, which is the same as none.
			});

		subtasks = [];
		comments = [];
		attachments = [];
		notes = [];
		reminders = [];

		const id = task.id;
		api.listTasks({ parent_id: id, completed: 'include' })
			.then((r) => id === seededId && (subtasks = r.tasks))
			.catch(() => {});
		api.listComments(id)
			.then((r) => {
				if (id !== seededId) return;
				comments = r.comments;
				attachments = r.attachments ?? [];
			})
			.catch(() => {});
		api.listReminders(id)
			.then((r) => id === seededId && (reminders = r.reminders))
			.catch(() => {});
		// Quietly, because a task with no notes is the ordinary case and a failure
		// here must not put an error in front of somebody reading a task.
		api.notesLinking('task', id)
			.then((r) => id === seededId && (notes = r.notes ?? []))
			.catch(() => {});
	});

	/**
	 * Sends a patch through the store, so the row behind the drawer updates too.
	 *
	 * `task` can be null by the time this runs. The three fields below save on
	 * blur, and closing the drawer *is* a blur — the input is being unmounted, and
	 * the browser fires blur on the way out. If the task left `app.tasks` in the
	 * same moment (it was completed, it was moved, another browser deleted it),
	 * `app.detailTask` is already null and this is a handler for a task that is no
	 * longer there.
	 *
	 * It threw `Cannot read properties of null` and stopped the page. Nothing to
	 * save is not an error; it is the ordinary end of an edit nobody made.
	 */
	async function save(patch) {
		if (!task) return;
		await app.update(task.id, patch);
	}

	/**
	 * Saves the title, reading it the way quick add would.
	 *
	 * Typing "i morgen kl. 14" into a title used to leave it as those words. The
	 * same sentence in the quick-add box becomes a date, which made where you
	 * typed it decide what it meant.
	 *
	 * The parse runs on the server — the grammar is Danish and English and lives
	 * there — and is only applied when it actually consumed something. What it
	 * took is said out loud, because a title that edits itself without a word is
	 * worse than one that does nothing.
	 */
	async function saveContent() {
		if (!task) return;
		const trimmed = content.trim();
		if (!trimmed || trimmed === task.content) {
			content = task.content;
			return;
		}

		let parsed = null;
		try {
			parsed = await api.quickAddPreview(trimmed);
		} catch {
			// Offline, or the parser refused. Saving the words is still right.
		}

		const patch = { content: trimmed };
		const took = [];
		if (parsed && parsed.content && parsed.content !== trimmed) {
			patch.content = parsed.content;
			if (parsed.due_date) {
				patch.due_date = parsed.due_date;
				took.push(parsed.due_date);
			}
			if (parsed.priority && parsed.priority < 4) {
				patch.priority = parsed.priority;
				took.push(`P${parsed.priority}`);
			}
			if (parsed.recurrence) {
				patch.recurrence_rule = parsed.recurrence;
				took.push('gentagelse');
			}
			if (parsed.labels?.length) {
				patch.labels = [...new Set([...(task.labels ?? []), ...parsed.labels])];
				took.push(parsed.labels.map((l) => `@${l}`).join(' '));
			}
		}

		await save(patch);

		// The fields are seeded once per task so a save cannot overwrite what
		// somebody is typing. That rule has to bend exactly here: this save
		// changed the very fields on screen, and leaving them showing what was
		// typed while the toast reports something else is the worst of both.
		if (took.length) {
			content = patch.content;
			if (patch.due_date) dueDate = patch.due_date;
			if (patch.priority) priority = patch.priority;
			if (patch.recurrence_rule) recurrence = patch.recurrence_rule;
			if (patch.labels) labels = patch.labels.join(', ');
			app.toast(t('detail.readAs', { what: took.join(', ') }));
		}
	}

	async function saveProject() {
		if (projectId === task.project_id) return;
		await save({ project_id: projectId });
	}

	async function saveRecurrence() {
		if (recurrence === (task.recurrence_rule ?? '')) return;
		await save({ recurrence_rule: recurrence });
	}

	async function saveAssignee() {
		if (assignee === (task.assignee_id ?? '')) return;
		await save({ assignee_id: assignee });
	}

	function saveDescription() {
		if (!task || description === (task.description ?? '')) return;
		save({ description });
	}

	function saveLabels() {
		if (!task) return;
		const list = labels
			.split(',')
			.map((l) => l.trim())
			.filter(Boolean);
		if (list.join() === (task.labels ?? []).join()) return;
		save({ labels: list });
	}

	// --- sub-tasks ----------------------------------------------------------------

	async function addSubtask(event) {
		event.preventDefault();
		const text = newSubtask.trim();
		if (!text) return;
		newSubtask = '';
		try {
			const created = await api.createTask({
				content: text,
				project_id: task.project_id,
				parent_id: task.id
			});
			subtasks = [...subtasks, created];
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function toggleSubtask(subtask) {
		const previous = subtasks;
		subtasks = subtasks.map((s) =>
			s.id === subtask.id ? { ...s, completed: !s.completed } : s
		);
		try {
			const updated = subtask.completed
				? await api.reopenTask(subtask.id)
				: await api.completeTask(subtask.id);
			subtasks = subtasks.map((s) => (s.id === updated.id ? updated : s));
		} catch (e) {
			subtasks = previous;
			app.toast(humanMessage(e));
		}
	}

	async function removeSubtask(subtask) {
		const previous = subtasks;
		subtasks = subtasks.filter((s) => s.id !== subtask.id);
		try {
			await api.deleteTask(subtask.id);
		} catch (e) {
			subtasks = previous;
			app.toast(humanMessage(e));
		}
	}

	// --- comments and files ---------------------------------------------------------

	async function addComment(event) {
		event.preventDefault();
		const body = newComment.trim();
		if (!body) return;
		posting = true;
		try {
			const created = await api.createComment(task.id, body);
			comments = [...comments, created];
			newComment = '';
		} catch (e) {
			app.toast(e.fields?.body ?? humanMessage(e));
		} finally {
			posting = false;
		}
	}

	async function removeComment(comment) {
		if (!confirm(t('detail.deleteComment'))) return;
		const previous = comments;
		comments = comments.filter((c) => c.id !== comment.id);
		try {
			await api.deleteComment(comment.id);
		} catch (e) {
			comments = previous;
			app.toast(humanMessage(e));
		}
	}

	async function upload(event) {
		const file = event.target.files?.[0];
		if (!file) return;
		event.target.value = '';

		uploading = true;
		try {
			attachments = [...attachments, await api.uploadAttachment(task.id, file)];
		} catch (e) {
			app.toast(e.fields?.file ?? humanMessage(e));
		} finally {
			uploading = false;
		}
	}

	async function removeAttachment(attachment) {
		const previous = attachments;
		attachments = attachments.filter((a) => a.id !== attachment.id);
		try {
			await api.deleteAttachment(attachment.id);
		} catch (e) {
			attachments = previous;
			app.toast(humanMessage(e));
		}
	}

	// --- reminders --------------------------------------------------------------------

	async function addReminder(event) {
		event.preventDefault();
		if (!newReminder) return;
		try {
			// The input gives local wall-clock time; the API wants an instant.
			const created = await api.createReminder(task.id, {
				remind_at: new Date(newReminder).toISOString()
			});
			reminders = [...reminders, created];
			newReminder = '';
		} catch (e) {
			app.toast(e.fields?.remind_at ?? humanMessage(e));
		}
	}

	async function removeReminder(reminder) {
		const previous = reminders;
		reminders = reminders.filter((r) => r.id !== reminder.id);
		try {
			await api.deleteReminder(reminder.id);
		} catch (e) {
			reminders = previous;
			app.toast(humanMessage(e));
		}
	}

	function onkeydown(event) {
		if (event.key === 'Escape') onclose?.();
	}

	const kb = (bytes) =>
		bytes < 1024 * 1024
			? `${Math.max(1, Math.round(bytes / 1024))} kB`
			: `${(bytes / (1024 * 1024)).toFixed(1)} MB`;

	const stamp = (iso) =>
		new Date(iso).toLocaleString('da-DK', {
			day: 'numeric',
			month: 'short',
			hour: '2-digit',
			minute: '2-digit'
		});
</script>

<svelte:window {onkeydown} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<div class="scrim" onclick={onclose} role="presentation"></div>

<aside class="drawer" aria-label={t('detail.task')}>
	<header>
		<button class="close" onclick={onclose} aria-label={t('detail.close')}>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="M6 6l12 12M18 6L6 18" />
			</svg>
		</button>
	</header>

	<div class="scroll">
		<div class="field">
			<input
				class="title"
				use:focusOnMount
				bind:value={content}
				onblur={saveContent}
				aria-label={t('detail.text')}
			/>
		</div>

		<div class="field">
			<label for="description">{t('detail.description')}</label>
			<textarea
				id="description"
				rows="4"
				bind:value={description}
				onblur={saveDescription}
				placeholder={t('detail.descriptionPlaceholder')}
			></textarea>
		</div>

		<div class="grid">
			<div class="field">
				<label for="priority">{t('detail.priority')}</label>
				<select
					id="priority"
					bind:value={priority}
					onchange={() => save({ priority: Number(priority) })}
				>
					<option value={1}>{t('detail.p1')}</option>
					<option value={2}>P2</option>
					<option value={3}>P3</option>
					<option value={4}>{t('detail.none')}</option>
				</select>
			</div>

			<div class="field">
				<label for="due">{t('detail.due')}</label>
				<input
					id="due"
					type="date"
					bind:value={dueDate}
					onchange={() => save({ due_date: dueDate })}
				/>
			</div>
		</div>

		<div class="field">
			<label for="labels">{t('detail.labels')}</label>
			<input
				id="labels"
				bind:value={labels}
				onblur={saveLabels}
				placeholder={t('detail.labelsPlaceholder')}
			/>
		</div>

		<div class="grid">
			<div class="field">
				<label for="project">{t('detail.project')}</label>
				<select id="project" bind:value={projectId} onchange={saveProject}>
					{#each app.projects as project (project.id)}
						<option value={project.id}>{projectName(project)}</option>
					{/each}
				</select>
			</div>

			<div class="field">
				<label for="repeat">{t('detail.repeats')}</label>
				<select id="repeat" bind:value={recurrence} onchange={saveRecurrence}>
					{#each REPEATS as option (option.rule)}
						<option value={option.rule}>{option.label}</option>
					{/each}
					<!-- A rule typed as "hver anden tirsdag" has no entry in the list;
					     showing it rather than snapping to the nearest preset is the
					     difference between a menu and a menu that quietly edits. -->
					{#if recurrence && !REPEATS.some((o) => o.rule === recurrence)}
						<option value={recurrence}>{task.recurrence_text ?? 'Som skrevet'}</option>
					{/if}
				</select>
			</div>
		</div>

		{#if members.length > 1}
			<div class="field">
				<label for="assignee">{t('detail.assignee')}</label>
				<select id="assignee" bind:value={assignee} onchange={saveAssignee}>
					<option value="">{t('detail.none')}</option>
					{#each members as member (member.user_id)}
						<option value={member.user_id}>{member.name}</option>
					{/each}
				</select>
			</div>
		{/if}

		<section>
			<h3>{t('detail.subtasks')}</h3>

			{#each subtasks as subtask (subtask.id)}
				<div class="subtask" class:done={subtask.completed}>
					<button
						class="check"
						class:checked={subtask.completed}
						onclick={() => toggleSubtask(subtask)}
						aria-label={subtask.completed ? t('detail.reopen') : t('task.complete')}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M6 12.5l4 4 8-8.5" />
						</svg>
					</button>
					<span class="subtask-text">{subtask.content}</span>
					<button class="remove" onclick={() => removeSubtask(subtask)} aria-label={t('detail.delete')}>
						×
					</button>
				</div>
			{/each}

			<form onsubmit={addSubtask}>
				<input bind:value={newSubtask} placeholder={t('detail.addSubtask')} aria-label={t('detail.newSubtask')} />
			</form>
		</section>

		<section>
			<h3>{t('detail.files')}</h3>

			{#each attachments as attachment (attachment.id)}
				<div class="file">
					<!-- Always a download, never rendered inline: an uploaded SVG shown
					     in place would run its own script on this origin. -->
					<a href={attachment.url} download={attachment.filename}>{attachment.filename}</a>
					<span class="size">{kb(attachment.size)}</span>
					<button class="remove" onclick={() => removeAttachment(attachment)} aria-label={t('detail.delete')}>
						×
					</button>
				</div>
			{/each}

			<label class="upload">
				<input type="file" onchange={upload} disabled={uploading} />
				<span>{uploading ? t('detail.uploading') : t('detail.attachFile')}</span>
			</label>
		</section>

		<section>
			<h3>{t('detail.reminders')}</h3>

			{#each reminders as reminder (reminder.id)}
				<div class="reminder">
					<span>{reminder.remind_at ? stamp(reminder.remind_at) : `${reminder.offset_min} min.`}</span>
					{#if reminder.sent}<span class="sent">{t('detail.sent')}</span>{/if}
					<button class="remove" onclick={() => removeReminder(reminder)} aria-label={t('detail.delete')}>
						×
					</button>
				</div>
			{/each}

			<form onsubmit={addReminder}>
				<input
					type="datetime-local"
					bind:value={newReminder}
					aria-label={t('detail.newReminder')}
				/>
				<button class="add" type="submit">{t('detail.add')}</button>
			</form>
		</section>

		<!-- What has been written about this task.
		     The other direction of the same link: a note that mentions a task shows
		     it, and this is the task showing the note. Without it the connection only
		     works if you happen to be reading the note — which is the half nobody
		     needs help with. -->
		{#if notes.length}
			<section class="linked-notes">
				<h3>{t('notes.title')}</h3>
				{#each notes as note (note.id)}
					<a class="linked-note" href="/noter?note={note.id}">
						<strong>{note.title || t('notes.untitled')}</strong>
					</a>
				{/each}
			</section>
		{/if}

		<section>
			<h3>{t('detail.comments')}</h3>

			{#each comments as comment (comment.id)}
				<article class="comment">
					<span class="avatar" style="background: {comment.user_color}">
						{comment.user_name?.[0]?.toUpperCase() ?? '?'}
					</span>
					<div class="said">
						<span class="who">
							{comment.user_name}
							<span class="when">{stamp(comment.created_at)}</span>
						</span>
						<p>{comment.body}</p>
						{#each comment.attachments ?? [] as attachment (attachment.id)}
							<a class="inline-file" href={attachment.url} download={attachment.filename}>
								{attachment.filename}
							</a>
						{/each}
					</div>
					{#if comment.user_id === app.user?.id}
						<button class="remove" onclick={() => removeComment(comment)} aria-label={t('detail.delete')}>
							×
						</button>
					{/if}
				</article>
			{/each}

			<form onsubmit={addComment}>
				<textarea
					rows="2"
					bind:value={newComment}
					placeholder={t('detail.writeComment')}
					aria-label={t('detail.newComment')}
				></textarea>
				<button class="add" type="submit" disabled={posting}>{t('detail.post')}</button>
			</form>
		</section>
	</div>
</aside>

<style>
	.linked-note {
		display: block;
		padding: var(--s2);
		border-radius: var(--radius);
		color: var(--ink-muted);
		text-decoration: none;
	}

	.linked-note:hover {
		background: var(--surface);
		color: var(--ink);
	}

	.linked-note strong {
		font-size: var(--text-sm);
		font-weight: 560;
	}

	.scrim {
		position: fixed;
		inset: 0;
		background: rgb(0 0 0 / 0.4);
		z-index: 70;
	}

	.drawer {
		position: fixed;
		inset: 0 0 0 auto;
		/* `100%` rather than `100vw`. For a fixed element a percentage resolves
		   against the initial containing block, which excludes the scrollbar gutter
		   — `100vw` does not, so the drawer was a few pixels wider than the page and
		   the whole document could be pushed sideways. */
		width: min(480px, 100%);
		max-width: 100%;
		z-index: 80;
		display: flex;
		flex-direction: column;
		background: var(--ground);
		border-left: 1px solid var(--line);
		box-shadow: var(--shadow-lg);
		animation: slide var(--medium) var(--ease-out);
	}

	@keyframes slide {
		from {
			transform: translateX(16px);
			opacity: 0;
		}
	}

	header {
		display: flex;
		justify-content: flex-end;
		padding: var(--s3) var(--s4);
		border-bottom: 1px solid var(--line);
		flex: none;
	}

	.close {
		width: 28px;
		height: 28px;
		display: grid;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-muted);
	}

	.close:hover {
		color: var(--ink);
		background: var(--surface);
	}

	.close svg {
		width: 16px;
		height: 16px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.75;
		stroke-linecap: round;
	}

	.scroll {
		flex: 1;
		min-width: 0;
		overflow-y: auto;
		/* Nothing inside may widen the drawer. A pasted URL in a comment or a long
		   unbroken word would otherwise stretch the flex column and take the page
		   with it. */
		overflow-x: hidden;
		overscroll-behavior: contain;
		padding: var(--s4) var(--s4) var(--s8);
		display: flex;
		flex-direction: column;
		gap: var(--s4);
		padding-bottom: max(var(--s8), env(safe-area-inset-bottom));
	}

	.field {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	label {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
	}

	input,
	textarea,
	select {
		width: 100%;
		padding: var(--s2) var(--s3);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
		transition: border-color var(--fast) var(--ease);
	}

	input:focus,
	textarea:focus,
	select:focus {
		border-color: var(--accent);
	}

	textarea {
		resize: vertical;
		font-family: inherit;
		line-height: 1.5;
	}

	/* The title is the one field that should not look like a field until you are in
	   it — it is a heading that happens to be editable. */
	.title {
		font-size: var(--text-lg);
		font-weight: 560;
		letter-spacing: -0.015em;
		background: transparent;
		border-color: transparent;
		padding-left: 0;
	}

	.title:hover {
		border-color: var(--line);
		padding-left: var(--s3);
	}

	.title:focus {
		background: var(--surface);
		padding-left: var(--s3);
	}

	.grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: var(--s3);
	}

	section {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		padding-top: var(--s3);
		border-top: 1px solid var(--line);
	}

	h3 {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		font-weight: 560;
	}

	.subtask,
	.file,
	.reminder {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
	}

	.subtask-text {
		flex: 1;
		min-width: 0;
		overflow-wrap: anywhere;
	}

	.subtask.done .subtask-text {
		color: var(--ink-faint);
		text-decoration: line-through;
	}

	.check {
		flex: none;
		width: 16px;
		height: 16px;
		border: 1.5px solid var(--line-strong);
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		transition:
			background var(--fast) var(--ease),
			border-color var(--fast) var(--ease);
	}

	.check:hover {
		border-color: var(--accent);
	}

	.check.checked {
		background: var(--accent);
		border-color: var(--accent);
	}

	.check svg {
		width: 11px;
		height: 11px;
		fill: none;
		stroke: var(--accent-ink);
		stroke-width: 3;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.file a,
	.inline-file {
		color: var(--accent);
		text-decoration: none;
		overflow-wrap: anywhere;
	}

	.file a:hover,
	.inline-file:hover {
		text-decoration: underline;
	}

	.file a {
		flex: 1;
		min-width: 0;
	}

	.size,
	.sent,
	.when {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.reminder span:first-child {
		flex: 1;
	}

	.remove {
		flex: none;
		width: 20px;
		height: 20px;
		border-radius: var(--radius-sm);
		color: var(--ink-faint);
		line-height: 1;
	}

	.remove:hover {
		color: var(--danger);
		background: var(--danger-sunken);
	}

	.upload {
		font-size: var(--text-sm);
		color: var(--accent);
		cursor: pointer;
		text-transform: none;
		letter-spacing: normal;
	}

	.upload input {
		display: none;
	}

	form {
		display: flex;
		gap: var(--s2);
		align-items: flex-start;
		margin-top: var(--s1);
	}

	.add {
		flex: none;
		padding: var(--s2) var(--s3);
		background: var(--accent);
		color: var(--accent-ink);
		border-radius: var(--radius);
		font-size: var(--text-sm);
	}

	.add:disabled {
		opacity: 0.5;
	}

	.comment {
		display: flex;
		gap: var(--s2);
		align-items: flex-start;
		padding: var(--s2) 0;
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

	.said {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.who {
		font-size: var(--text-xs);
		color: var(--ink-muted);
		display: flex;
		gap: var(--s2);
	}

	.said p {
		margin: 0;
		font-size: var(--text-sm);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	@media (max-width: 560px) {
		/* Full width, stated as four insets rather than a width: the box is then
		   defined by the viewport's edges and cannot be a rounding error wider than
		   the thing it is pinned to. */
		.drawer {
			inset: 0;
			width: auto;
			border-left: 0;
		}
	}
</style>
