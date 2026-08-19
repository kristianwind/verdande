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
	import { t, tag } from '$lib/i18n.svelte.js';
	import { NOTE, startDrag } from '$lib/dnd.js';
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

	/**
	 * Hvornår, skrevet som man ville sige det.
	 *
	 * "i går" og et klokkeslæt i dag; en dato, når det er længere siden. En note
	 * fra i formiddags og en fra i fjor skal ikke se ens ud i en liste, man skanner.
	 */
	function when(iso) {
		if (!iso) return '';
		const at = new Date(iso);
		const now = new Date();
		const sameDay = at.toDateString() === now.toDateString();
		if (sameDay) return at.toLocaleTimeString(tag(), { hour: '2-digit', minute: '2-digit' });

		const yesterday = new Date(now);
		yesterday.setDate(now.getDate() - 1);
		if (at.toDateString() === yesterday.toDateString()) return t('notes.yesterday');

		return at.toLocaleDateString(tag(), { day: '2-digit', month: '2-digit', year: 'numeric' });
	}

	function projectName(id) {
		return app.projects.find((p) => p.id === id)?.name ?? '';
	}

	// Trukket ind på et projekt i sidebjælken. Nyttelasten er notens id; sidebjælken
	// kender allerede formen fra opgaver.
	let dragging = $state(null);

	function onDragStart(event, note) {
		dragging = note.id;
		startDrag(event, NOTE, note.id);
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
							<button
								class="row"
								class:on={selected?.id === note.id}
								onclick={() => open(note)}
								draggable="true"
								ondragstart={(e) => onDragStart(e, note)}
								ondragend={() => (dragging = null)}
							>
								<strong>{note.title || t('notes.untitled')}</strong>
								<!-- Dato og begyndelse på samme linje, som Apple Noter gør det: to
								     linjer pr. note frem for tre, og datoen er dét, man skanner
								     efter, når man leder efter noget, man skrev i tirsdags. -->
								<span class="under">
									<span class="when">{when(note.updated_at)}</span>
									<span class="preview">{plain(note.body)}</span>
								</span>
								{#if note.project_id}
									<span class="filed">{projectName(note.project_id)}</span>
								{/if}
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
		/* En grid-kolonne er som udgangspunkt mindst så bred som sit bredeste
		   indhold, og et API-token uden et mellemrum i er ét meget bredt ord. Uden
		   min-width: 0 skubber sådan en note hele listen bredere, og editoren
		   klemmes sammen — på en note man ikke engang har åben. */
		min-width: 0;

		/* Air on all sides. The heading, the search box and the list all sat flush
		   against the sidebar's rule, which reads as a rendering fault rather than
		   as a layout. */
		/* Luft hele vejen rundt om arket. Højre og bund var nul, dengang editoren
		   ingen kant havde og ingenting kunne støde imod. */
		padding: var(--s3) var(--s4) var(--s3) var(--s4);
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
		min-width: 0;
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

	/* Tegnet af os, ikke af browseren.
	   Safari giver type="search" sit eget udseende — pilleform og forstørrelsesglas
	   — og skifter til det almindelige, så snart feltet får fokus: ikonet forsvandt
	   og formen blev firkantet midt i et klik. Med appearance: none er der kun én
	   udgave, og den ser ens ud hele tiden. */
	.search {
		width: 100%;
		appearance: none;
		-webkit-appearance: none;
		padding-left: 30px;
		border-radius: 999px;
		background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%238b918d' stroke-width='2' stroke-linecap='round'%3E%3Ccircle cx='11' cy='11' r='7'/%3E%3Cpath d='M20 20l-3.5-3.5'/%3E%3C/svg%3E");
		background-repeat: no-repeat;
		background-position: 9px center;
		background-size: 15px 15px;
	}

	/* Safaris egen ryd-knap ville sidde oven i vores egen form. */
	.search::-webkit-search-decoration,
	.search::-webkit-search-cancel-button {
		-webkit-appearance: none;
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
		padding: var(--s2) var(--s2) var(--s3);
		border-radius: var(--radius);
		color: var(--ink-muted);
		border-bottom: 1px solid var(--line);
	}

	li:last-child .row {
		border-bottom: 0;
	}

	.row:hover {
		background: var(--surface);
	}

	.row.on {
		background: var(--surface-raised);
		color: var(--ink);
	}

	/* Samme størrelse som en opgavetitel. En note og en opgave er to ting af samme
	   slags — noget, der står på en liste og skal læses på et blik — og at give dem
	   hver sin størrelse får den ene til at se mindre vigtig ud end den anden. */
	.row strong {
		display: block;
		font-size: var(--text-sm);
		font-weight: 600;
		line-height: 1.45;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Dato og begyndelse på samme linje, som Apple Noter gør det. To linjer pr.
	   note frem for tre, og datoen først, fordi det er den, man skanner efter. */
	.under {
		display: flex;
		gap: var(--s2);
		min-width: 0;
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.when {
		flex: none;
		color: var(--ink-muted);
	}

	.preview {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Hvor den ligger, med en mappe foran — Apple Noters egen måde at sige det på,
	   og den eneste linje her, der ikke er notens eget indhold. */
	.filed {
		display: block;
		margin-top: 2px;
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.filed::before {
		content: '▸ ';
		opacity: 0.7;
	}

	/* Noten er et ark, ikke en flade i appen.
	 *
	 * Apple Notes skriver på hvidt, og det er ikke pynt: et ark, der holder op et
	 * sted, fortæller hvor teksten hører til, og skiller det, man skriver, fra det
	 * program, man skriver det i. Uden det flød noten ud i siden og lignede endnu
	 * en rude.
	 *
	 * `--surface` frem for `#fff`, fordi der er fem temaer. Rollen er "arket løftet
	 * fra grunden" — hvid i lyst, cremet i paper, en anelse lysere end grunden i de
	 * mørke. Et hårdkodet hvidt ville være et blændende ark i mørkt tema. */
	.editor {
		display: flex;
		flex-direction: column;
		min-height: 0;
		min-width: 0;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		padding: var(--s3) var(--s4);
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
