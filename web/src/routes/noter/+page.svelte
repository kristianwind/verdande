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
	import { t } from '$lib/i18n.svelte.js';
	import { linesOf } from '$lib/markdown.js';

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

	// What the note points at, for the panel under the editor.
	let links = $derived(selected?.links ?? []);

	// The styled twin of what is in the textarea. Recomputed on every keystroke,
	// which sounds expensive and is not: it is a split and a handful of regexes
	// over a page of text, and it runs in the same frame as the character that
	// caused it.
	let lines = $derived(linesOf(draft));
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
						<button class="row" class:on={selected?.id === note.id} onclick={() => open(note)}>
							<strong>{note.title || t('notes.untitled')}</strong>
							<span class="preview">{note.body.slice(0, 80)}</span>
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	</aside>

	<section class="editor">
		{#if selected}
			<!-- The mirror sits behind the textarea and must match it exactly: same font,
			     same size, same padding, same wrapping. Any difference shows up as
			     formatting that drifts away from the text as the note grows.

			     A contenteditable would let the text be styled directly and would also
			     break undo, autocorrect and IME input — a bad trade in the one place in
			     this program where somebody writes for half an hour at a stretch. -->
			<div class="paper">
				<div class="mirror" aria-hidden="true">
					{#each lines as line}
						<div class={line.block}>
							{#each line.parts as part}
								{#if part.kind}<span class={part.kind}>{part.text}</span>{:else}{part.text}{/if}
							{/each}{#if !line.parts.length}&nbsp;{/if}
						</div>
					{/each}
				</div>

				<textarea
					bind:value={draft}
					oninput={typed}
					onblur={save}
					spellcheck="true"
					aria-label={t('notes.body')}
					placeholder={t('notes.placeholder')}
				></textarea>
			</div>

			<footer>
				<span class="hint">{saving ? t('notes.saving') : t('notes.saved')}</span>
				{#if links.length}
					<span class="links">
						{#each links as link}
							<span class="link">{link.kind === 'project' ? '#' : ''}{link.target_id}</span>
						{/each}
					</span>
				{/if}
				<button class="remove" onclick={() => remove(selected)}>{t('notes.delete')}</button>
			</footer>
		{:else}
			<p class="empty">{t('notes.pickOne')}</p>
		{/if}
	</section>
</div>

<style>
	.notes {
		/* Air above the heading, so it does not sit against the top bar. */
		padding-top: var(--s3);
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

	.row {
		display: block;
		width: 100%;
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

	.remove {
		margin-left: auto;
		color: var(--ink-faint);
	}

	.remove:hover {
		color: var(--danger);
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
