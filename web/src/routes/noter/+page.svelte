<script>
	/**
	 * Notes.
	 *
	 * A list on the left, the note itself on the right. Not a grid of cards: a note
	 * is read by its first line far more often than by its whole self, and a list
	 * shows twenty of those where a grid shows six.
	 *
	 * Markdown in a plain textarea for now. The live preview — where the marks show
	 * only in the line the cursor is in — is the next piece and the expensive one;
	 * this is the shape it will sit inside, and everything else is already true
	 * without it.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { page } from '$app/stores';
	import { t } from '$lib/i18n.svelte.js';
	import NoteEditor from '$lib/components/NoteEditor.svelte';

	let notes = $state([]);
	let selected = $state(null);
	let query = $state('');
	let status = $state('loading');
	let saving = $state(false);

	// The text being edited, kept apart from the note it came from so a save that
	// arrives late cannot overwrite what has been typed since.
	let draft = $state('');
	let timer;

	$effect(() => {
		load(query);
	});

	// Arriving from somewhere that names a note — a task's panel, a project's page —
	// opens it rather than dropping the person on a list to find it again.
	let asked = $derived($page.url.searchParams.get('note'));
	$effect(() => {
		if (!asked || selected?.id === asked) return;
		const found = notes.find((n) => n.id === asked);
		if (found) open(found);
		else api.note(asked).then(open).catch(() => {});
	});

	async function load(q) {
		try {
			notes = (await api.notes(q ? { q } : {})).notes ?? [];
			status = 'ready';
			if (selected && !notes.some((n) => n.id === selected.id)) open(notes[0] ?? null);
		} catch (e) {
			status = 'failed';
			app.toast(humanMessage(e));
		}
	}

	function open(note) {
		clearTimeout(timer);
		selected = note;
		draft = note?.body ?? '';
	}

	/**
	 * The first line or so of a note, without its marks.
	 *
	 * The list showed the Markdown as stored — "# Møde med Anders" and "**fed**" —
	 * which is exactly the thing the editor was built to stop showing. What is
	 * wanted here is the sentence, not the notation.
	 */
	function plain(body) {
		return (body ?? '')
			.replace(/^#{1,6}\s+/gm, '')
			.replace(/^\s*[-*+]\s+/gm, '')
			.replace(/^\s*>\s?/gm, '')
			.replace(/\*\*([^*]+)\*\*/g, '$1')
			.replace(/~~([^~]+)~~/g, '$1')
			.replace(/<\/?u>/g, '')
			.replace(/`([^`]+)`/g, '$1')
			.replace(/\*([^*]+)\*/g, '$1')
			.replace(/\[\[([^\]]+)\]\]/g, '$1')
			.replace(/\s+/g, ' ')
			.trim()
			.slice(0, 90);
	}

	async function create() {
		try {
			const note = await api.createNote({ body: '' });
			notes = [note, ...notes];
			open(note);
			document.querySelector('.editor textarea')?.focus();
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	/**
	 * Saved as you stop typing rather than on a button.
	 *
	 * A note is the one place in this program where an hour of writing can be lost
	 * to one mis-click, and a Gem-knap is a button that exists to be forgotten. The
	 * delay is long enough that a sentence is one save and not twenty.
	 */
	function typed() {
		clearTimeout(timer);
		timer = setTimeout(save, 700);
	}

	async function save() {
		if (!selected) return;
		const id = selected.id;
		const body = draft;
		saving = true;
		try {
			const saved = await api.updateNote(id, { body });
			notes = notes.map((n) => (n.id === id ? saved : n));
			// Only if it is still the one on screen: switching notes mid-save must not
			// drag the previous one's title back onto this one.
			if (selected?.id === id) selected = saved;
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			saving = false;
		}
	}

	/**
	 * Favourites are the pinned ones. The list already puts them first, so marking
	 * one is the same act as saying "keep this where I can see it" — one idea, not
	 * two that have to be explained apart.
	 */
	async function favourite(note) {
		const pinned = !note.pinned;
		// Moved before the answer comes back: a star that waits for a round trip
		// feels broken on a slow connection, and the cost of being wrong is a star.
		notes = notes.map((n) => (n.id === note.id ? { ...n, pinned } : n));
		try {
			await api.updateNote(note.id, { pinned });
			await load(query);
		} catch (e) {
			notes = notes.map((n) => (n.id === note.id ? { ...n, pinned: !pinned } : n));
			app.toast(humanMessage(e));
		}
	}

	/**
	 * Moves the note into a project, which is what sharing it means: everybody who
	 * can read the project can read the note, and an editor can change it. Choosing
	 * the empty option takes it back out and makes it the author's alone again.
	 */
	async function share(projectId) {
		if (!selected) return;
		const id = selected.id;
		try {
			const saved = await api.updateNote(id, { project_id: projectId });
			notes = notes.map((n) => (n.id === id ? saved : n));
			if (selected?.id === id) selected = saved;
			app.toast(projectId ? t('notes.shared') : t('notes.unshared'));
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function remove(note) {
		if (!confirm(t('notes.deleteNote', { name: note.title || t('notes.untitled') }))) return;
		try {
			await api.deleteNote(note.id);
			notes = notes.filter((n) => n.id !== note.id);
			if (selected?.id === note.id) open(notes[0] ?? null);
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	/**
	 * What a link is called on screen.
	 *
	 * A project tag is stored folded, because it is a key — but showing the folded
	 * key would mean a note that says #garageristeriet is labelled with a spelling
	 * that appears nowhere, neither in the note nor on the project. The project's
	 * own name is the right answer, and the fold is what makes it findable.
	 */
	function label(link) {
		if (link.kind !== 'project') return link.target_id;
		const project = app.projects.find(
			(p) => p.name.toLowerCase() === link.target_id.toLowerCase()
		);
		return '#' + (project?.name ?? link.target_id);
	}

	// What the note points at, for the panel under the editor.
	let links = $derived(selected?.links ?? []);


</script>

<svelte:head><title>{t('notes.title')} · verdande</title></svelte:head>

<div class="notes">
	<aside>
		<div class="head">
			<h1>{t('notes.title')}</h1>
			<button class="new" onclick={create} aria-label={t('notes.new')}>+</button>
		</div>

		<input
			class="search"
			type="search"
			bind:value={query}
			placeholder={t('notes.search')}
			aria-label={t('notes.search')}
		/>

		{#if status === 'loading'}
			<p class="hint">{t('history.loading')}</p>
		{:else if !notes.length}
			<p class="hint">{query ? t('notes.noneFound') : t('notes.noneYet')}</p>
		{:else}
			<ul>
				{#each notes as note (note.id)}
					<li>
						<div class="rowline">
							<button class="row" class:on={selected?.id === note.id} onclick={() => open(note)}>
								<strong>{note.title || t('notes.untitled')}</strong>
								<span class="preview">{plain(note.body)}</span>
							</button>
							<button
								class="star"
								class:on={note.pinned}
								onclick={() => favourite(note)}
								aria-pressed={note.pinned}
								aria-label={note.pinned ? t('notes.unfavourite') : t('notes.favourite')}
								title={note.pinned ? t('notes.unfavourite') : t('notes.favourite')}
							>
								{note.pinned ? '★' : '☆'}
							</button>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</aside>

	<section class="editor">
		{#if selected}
			<NoteEditor
				note={selected}
				onchange={(body) => {
					draft = body;
					typed();
				}}
				onsave={save}
			/>

			<footer>
				<span class="hint">{saving ? t('notes.saving') : t('notes.saved')}</span>
				{#if links.length}
					<span class="links">
						{#each links as link}
							<span class="link">{label(link)}</span>
						{/each}
					</span>
				{/if}
				<!-- Sharing a note is filing it in a project.
				     Not an access list of its own: projects already have members, roles
				     and invitations, all of them tested and all of them things people
				     have already learnt here. A second way to give somebody access is a
				     second place to get it wrong, and the two would disagree on the day
				     one of them was changed. -->
				<label class="share">
					<span class="hint">{t('notes.shareWith')}</span>
					<select value={selected.project_id ?? ''} onchange={(e) => share(e.currentTarget.value)}>
						<option value="">{t('notes.private')}</option>
						{#each app.projects.filter((p) => !p.is_inbox) as project (project.id)}
							<option value={project.id}>{project.name}</option>
						{/each}
					</select>
				</label>

				<div class="actions">
					<button class="button" onclick={save}>{t('notes.save')}</button>
					<button class="button danger" onclick={() => remove(selected)}>{t('notes.delete')}</button>
				</div>
			</footer>
		{:else}
			<p class="empty">{t('notes.pickOne')}</p>
		{/if}
	</section>
</div>

<style>
	.notes {
		/* Air on all sides. The heading, the search box and the list all sat flush
		   against the sidebar's rule, which reads as a rendering fault rather than
		   as a layout. */
		padding: var(--s3) 0 0 var(--s4);
		display: grid;
		grid-template-columns: minmax(220px, 300px) 1fr;
		gap: var(--s4);
		height: 100%;
		min-height: 0;
	}

	aside {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		min-height: 0;
		border-right: 1px solid var(--line);
		padding-right: var(--s3);
	}

	.head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
	}

	h1 {
		font-size: var(--text-xl);
		line-height: 1.2;
	}

	.new {
		font-size: var(--text-lg);
		color: var(--ink-faint);
		padding: 0 var(--s2);
	}

	.new:hover {
		color: var(--ink);
	}

	.search {
		width: 100%;
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		overflow-y: auto;
		min-height: 0;
	}

	.rowline {
		display: flex;
		align-items: center;
		gap: var(--s1);
	}

	.star {
		flex: none;
		width: 24px;
		color: var(--ink-faint);
		opacity: 0;
		font-size: var(--text-sm);
	}

	.rowline:hover .star,
	.star.on,
	.star:focus-visible {
		opacity: 1;
	}

	.star.on {
		color: var(--accent);
	}

	.row {
		display: block;
		flex: 1;
		min-width: 0;
		text-align: left;
		padding: var(--s2);
		border-radius: var(--radius);
		color: var(--ink-muted);
	}

	.row:hover {
		background: var(--surface);
	}

	.row.on {
		background: var(--surface-raised);
		color: var(--ink);
	}

	.row strong {
		display: block;
		font-size: var(--text-sm);
		font-weight: 560;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.preview {
		display: block;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.editor {
		display: flex;
		flex-direction: column;
		min-height: 0;
	}

	/* Everything below exists to keep two layers in lockstep. The shared rules are
	   set on both at once on purpose: a font-size on one and not the other is a
	   whole line of drift by the bottom of a long note. */
	.paper {
		position: relative;
		flex: 1;
		min-height: 50vh;
	}

	.paper textarea,
	.paper .mirror {
		font: inherit;
		font-size: var(--text-base, 1rem);
		line-height: 1.6;
		padding: var(--s2) 0;
		border: 0;
		white-space: pre-wrap;
		overflow-wrap: break-word;
		word-break: normal;
		tab-size: 4;
	}

	textarea {
		position: relative;
		width: 100%;
		height: 100%;
		resize: none;
		background: none;
		/* Transparent, so what is read is the mirror underneath. The caret keeps its
		   own colour or there would be nothing to type against. */
		color: transparent;
		caret-color: var(--ink);
	}

	textarea::selection {
		background: var(--accent);
		color: var(--accent-ink);
	}

	textarea:focus {
		outline: none;
	}

	.mirror {
		position: absolute;
		inset: 0;
		pointer-events: none;
		color: var(--ink);
		overflow: hidden;
	}

	/* Headings change weight and colour, never size: a larger font would take more
	   lines than the textarea does and everything below it would slide. */
	.mirror .h1,
	.mirror .h2,
	.mirror .h3,
	.mirror .h4,
	.mirror .h5,
	.mirror .h6 {
		font-weight: 620;
	}

	.mirror .h1 {
		color: var(--ink);
	}

	.mirror .quote {
		color: var(--ink-muted);
	}

	.mirror .bold {
		font-weight: 620;
	}

	.mirror .italic {
		font-style: italic;
	}

	.mirror .code {
		font-family: var(--mono, ui-monospace, monospace);
		color: var(--accent);
	}

	.mirror .tag,
	.mirror .wikilink {
		color: var(--accent);
	}

	footer {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding-top: var(--s2);
		border-top: 1px solid var(--line);
		font-size: var(--text-xs);
	}

	.links {
		display: flex;
		flex-wrap: wrap;
		gap: var(--s1);
	}

	.link {
		color: var(--ink-muted);
		background: var(--surface);
		border-radius: var(--radius-sm);
		padding: 0 var(--s1);
		font-family: var(--mono, ui-monospace, monospace);
	}

	.share {
		display: flex;
		align-items: center;
		gap: var(--s1);
	}

	.share select {
		font-size: var(--text-xs);
		padding: 2px var(--s1);
	}

	/* Real buttons. They were bare words, which on a Mac reads as a web page rather
	   than as something you can press. */
	.actions {
		margin-left: auto;
		display: flex;
		gap: var(--s2);
	}

	.button {
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		padding: var(--s1) var(--s3);
		background: var(--surface);
		color: var(--ink);
		font-size: var(--text-xs);
	}

	.button:hover {
		background: var(--surface-raised);
	}

	.button.danger {
		color: var(--danger);
	}

	.button.danger:hover {
		border-color: var(--danger);
	}

	.empty,
	.hint {
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}

	@media (max-width: 700px) {
		.notes {
			grid-template-columns: 1fr;
		}

		aside {
			border-right: 0;
			padding-right: 0;
		}
	}
</style>
