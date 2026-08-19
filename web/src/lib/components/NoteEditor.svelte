<script>
	/**
	 * The note itself.
	 *
	 * Rich text, not Markdown source: somebody who writes in Apple Notes presses
	 * ⌘B and expects the word to go bold, not to grow asterisks. The file underneath
	 * is still Markdown — see $lib/richtext.js for the seam — so nothing about the
	 * export changes.
	 *
	 * contenteditable with execCommand. It is deprecated and it is also the only
	 * thing every browser agrees on; writing a selection model from scratch is weeks
	 * of work and a new class of bug in the one place where losing text is
	 * unforgivable. When the replacement (the EditContext API) is everywhere, this
	 * is the file to change.
	 */
	import { markdownToHtml, htmlToMarkdown } from '$lib/richtext.js';
	import { highlight, guessLanguage } from '$lib/highlight.js';
	import { t } from '$lib/i18n.svelte.js';
	import { app } from '$lib/stores.svelte.js';
	import { colorVar } from '$lib/colors.js';

	let { note, onchange, onsave } = $props();

	let editor;
	let stylesOpen = $state(false);
	// Which marks are on where the caret is, so the toolbar shows state rather than
	// only offering actions.
	let active = $state({});

	// Loaded when the note changes, never while it is being typed in: writing to
	// innerHTML puts the caret back at the start, which mid-sentence is unusable.
	let loadedId = $state(null);
	$effect(() => {
		if (!editor || !note || note.id === loadedId) return;
		// An empty note opens on its title. That is what Apple Notes does, and it is
		// also what the list needs: the first line becomes the name of the note, so a
		// note whose first line is body text is a note called by its first sentence.
		//
		// Tested on the text and not on the rendered result: an empty body renders as
		// one empty paragraph, not as nothing, so a falsy check here never fired.
		editor.innerHTML = note.body.trim() ? markdownToHtml(note.body) : '<h1><br></h1>';
		loadedId = note.id;
		active = {};
		colourCode();
	});

	const SYNTAX = [
		{ mark: '#', what: 'notes.syntaxProject' },
		{ mark: '[[', what: 'notes.syntaxNote' },
		{ mark: '⌘B', what: 'notes.bold' },
		{ mark: '⌘U', what: 'notes.underline' }
	];

	const STYLES = [
		{ key: 'title', tag: 'h1', label: 'notes.styleTitle' },
		{ key: 'heading', tag: 'h2', label: 'notes.styleHeading' },
		{ key: 'subheading', tag: 'h3', label: 'notes.styleSubheading' },
		{ key: 'body', tag: 'p', label: 'notes.styleBody' },
		{ key: 'mono', tag: 'pre', label: 'notes.styleMono' },
		{ key: 'quote', tag: 'blockquote', label: 'notes.styleQuote' }
	];

	function apply(command, value) {
		editor?.focus();
		// Tags, not inline styles. Left to itself the browser writes
		// <span style="font-weight: normal"> for an un-bold, which is invisible to a
		// converter that reads tags — so the change is lost on the way to Markdown
		// and comes back bold. Asking for tags keeps the document in a shape that
		// can be written down.
		try {
			document.execCommand('styleWithCSS', false, false);
		} catch {
			// Not everywhere. The formats below still work; some of them just produce
			// spans, which is why the converter reads styles as well as tags.
		}
		document.execCommand(command, false, value);
		readState();
		changed();
	}

	function setStyle(style) {
		stylesOpen = false;
		if (style.key === 'bullet') return apply('insertUnorderedList');
		if (style.key === 'numbered') return apply('insertOrderedList');
		apply('formatBlock', style.tag);
	}

	/** What the caret is standing in, for the toolbar's pressed states. */
	function readState() {
		if (!editor) return;
		try {
			active = {
				bold: document.queryCommandState('bold'),
				italic: document.queryCommandState('italic'),
				underline: document.queryCommandState('underline'),
				strike: document.queryCommandState('strikeThrough'),
				bullet: document.queryCommandState('insertUnorderedList'),
				numbered: document.queryCommandState('insertOrderedList')
			};
		} catch {
			// Some browsers throw on queryCommandState with no selection. The toolbar
			// showing nothing is a smaller problem than the editor throwing.
			active = {};
		}
	}

	/**
	 * Farver kodeblokkene.
	 *
	 * Kun dem, markøren ikke står i. At farve blokken, mens nogen skriver i den,
	 * betyder at bygge dens indhold om ved hvert tastetryk og sætte markøren
	 * tilbage bagefter — det er den slags, der virker i ni tilfælde ud af ti og
	 * flytter markøren midt i en sætning i det tiende. Blokken er mørk og
	 * monospace hele tiden; farverne kommer, når man forlader den, hvilket er når
	 * man læser den.
	 */
	function colourCode() {
		if (!editor) return;
		const inside = currentBlock();
		for (const pre of editor.querySelectorAll('pre')) {
			if (pre === inside || pre.contains(inside)) continue;
			const code = pre.textContent ?? '';
			const lang = pre.getAttribute('data-lang') || guessLanguage(code);
			if (lang) pre.setAttribute('data-lang', lang);
			const painted = highlight(code, lang);
			// Kun hvis der er noget at ændre: at skrive innerHTML uændret ville
			// stadig rive noden op og lægge den tilbage, og det ses som et blink.
			const target = pre.querySelector('code') ?? pre;
			if (target.innerHTML !== painted) target.innerHTML = painted;
		}
	}

	function changed() {
		if (!editor) return;
		onchange?.(htmlToMarkdown(editor));
		readSuggestions();
	}

	// --- suggesting a project ------------------------------------------------------

	let suggestions = $state([]);
	let chosen = $state(0);

	/**
	 * The word being typed after a #, if that is what is happening.
	 *
	 * Read from the text node the caret is in rather than from the whole document:
	 * a note can mention twenty projects, and only the one under the cursor is being
	 * written.
	 */
	function partialTag() {
		const selection = window.getSelection();
		const node = selection?.anchorNode;
		if (!node || node.nodeType !== Node.TEXT_NODE || !selection.isCollapsed) return null;

		const before = node.textContent.slice(0, selection.anchorOffset);
		const match = /(?:^|\s)#([\p{L}\p{N}_-]*)$/u.exec(before);
		if (!match) return null;
		return { node, start: selection.anchorOffset - match[1].length, term: match[1] };
	}

	function readSuggestions() {
		const partial = partialTag();
		if (!partial) {
			suggestions = [];
			return;
		}
		const term = partial.term.toLowerCase();
		suggestions = app.projects
			.filter((p) => !p.is_inbox && p.name.toLowerCase().includes(term))
			.slice(0, 6);
		chosen = 0;
	}

	/** Replaces the half-typed tag with the whole name. */
	function accept(project) {
		const partial = partialTag();
		if (!partial) return;

		const range = document.createRange();
		range.setStart(partial.node, partial.start);
		range.setEnd(partial.node, partial.start + partial.term.length);
		const selection = window.getSelection();
		selection.removeAllRanges();
		selection.addRange(range);

		// A trailing space, because the next thing typed is a word and not more of
		// the tag — and without it the suggestions come straight back.
		document.execCommand('insertText', false, project.name + ' ');
		suggestions = [];
		onchange?.(htmlToMarkdown(editor));
	}

	/** The block the caret is standing in. */
	function currentBlock() {
		const selection = window.getSelection();
		if (!selection || !selection.anchorNode || !editor) return null;
		let node = selection.anchorNode;
		while (node && node.parentNode !== editor) node = node.parentNode;
		return node?.nodeType === Node.ELEMENT_NODE ? node : null;
	}

	/**
	 * Title for the first line, body for everything after it.
	 *
	 * Run after the browser has split the block on Enter, because until then there
	 * is no second block to change. Pressing return at the end of a heading leaves
	 * the caret in another heading in most browsers, and a note where every line is
	 * a title is a note with no title at all.
	 */
	function keepTitleFirst() {
		const block = currentBlock();
		if (!block) return;

		// A heading that is not the first line becomes body.
		if (block.tagName === 'H1' && block !== editor.firstElementChild) {
			document.execCommand('formatBlock', false, 'p');
			return;
		}

		// And the browser's own container becomes a paragraph. Chromium answers Enter
		// with a bare <div>, which reads as body but is not styled as one — so the
		// spacing between paragraphs quietly disappears the moment somebody presses
		// return rather than choosing Brødtekst from the menu.
		if (block.tagName === 'DIV') {
			document.execCommand('formatBlock', false, 'p');
		}
	}

	function onkeydown(event) {
		// The suggestion list owns the arrows and return while it is open, the way
		// every other completion does. Escape closes it without choosing.
		if (suggestions.length) {
			if (event.key === 'ArrowDown') {
				event.preventDefault();
				chosen = (chosen + 1) % suggestions.length;
				return;
			}
			if (event.key === 'ArrowUp') {
				event.preventDefault();
				chosen = (chosen - 1 + suggestions.length) % suggestions.length;
				return;
			}
			if (event.key === 'Enter' || event.key === 'Tab') {
				event.preventDefault();
				accept(suggestions[chosen]);
				return;
			}
			if (event.key === 'Escape') {
				event.preventDefault();
				suggestions = [];
				return;
			}
		}

		if (event.key === 'Enter' && !event.shiftKey) {
			// After the split, not before it.
			setTimeout(() => {
				keepTitleFirst();
				changed();
			}, 0);
			return;
		}
		// The shortcuts people already have in their fingers. execCommand handles
		// bold and italic itself; underline and strikethrough are claimed here so
		// they behave the same way.
		if (!(event.metaKey || event.ctrlKey)) return;
		const key = event.key.toLowerCase();
		if (key === 'u') {
			event.preventDefault();
			apply('underline');
		} else if (key === 'x' && event.shiftKey) {
			event.preventDefault();
			apply('strikeThrough');
		}
	}

	// Pasted text arrives as whatever the source was — a web page brings its fonts,
	// its colours and its layout with it. Only the words are wanted.
	function onpaste(event) {
		event.preventDefault();
		const text = event.clipboardData?.getData('text/plain') ?? '';
		document.execCommand('insertText', false, text);
		changed();
	}
</script>

<div class="wrap">
	<!-- Nothing in here may take focus. A click that moves focus out of the editor
	     collapses the selection first, so the format lands on nothing — the button
	     appears to do nothing at all, which is how it read the first time. -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="toolbar" onmousedown={(e) => e.preventDefault()}>
		<div class="styles">
			<button
				class="aa"
				onclick={() => (stylesOpen = !stylesOpen)}
				aria-expanded={stylesOpen}
				aria-label={t('notes.format')}
				title={t('notes.format')}>Aa</button
			>
			{#if stylesOpen}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div class="menu" role="menu">
					{#each STYLES as style}
						<button class={style.key} role="menuitem" onclick={() => setStyle(style)}>
							{t(style.label)}
						</button>
					{/each}
					<hr />
					<button
						class:on={active.bullet}
						role="menuitem"
						onclick={() => setStyle({ key: 'bullet' })}>• {t('notes.styleBullet')}</button
					>
					<button
						class:on={active.numbered}
						role="menuitem"
						onclick={() => setStyle({ key: 'numbered' })}>1. {t('notes.styleNumbered')}</button
					>
				</div>
			{/if}
		</div>

		<span class="sep" aria-hidden="true"></span>

		<button class:on={active.bold} onclick={() => apply('bold')} title="⌘B" aria-label={t('notes.bold')}>
			<strong>B</strong>
		</button>
		<button class:on={active.italic} onclick={() => apply('italic')} title="⌘I" aria-label={t('notes.italic')}>
			<em>I</em>
		</button>
		<button class:on={active.underline} onclick={() => apply('underline')} title="⌘U" aria-label={t('notes.underline')}>
			<u>U</u>
		</button>
		<button class:on={active.strike} onclick={() => apply('strikeThrough')} aria-label={t('notes.strike')}>
			<s>S</s>
		</button>
	</div>

	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		class="page"
		bind:this={editor}
		contenteditable="true"
		role="textbox"
		aria-multiline="true"
		aria-label={t('notes.body')}
		spellcheck="true"
		oninput={changed}
		onblur={() => {
			colourCode();
			onsave?.();
		}}
		onkeyup={() => {
			readState();
			readSuggestions();
		}}
		onclick={colourCode}
		onmouseup={readState}
		{onkeydown}
		{onpaste}
	></div>

	{#if suggestions.length}
		<!-- Anchored to the editor rather than to the caret. Following the caret means
		     measuring it, and a list that jumps a few pixels as you type is worse than
		     one that sits still. -->
		<ul class="suggestions" role="listbox">
			{#each suggestions as project, i (project.id)}
				<li>
					<button
						class:on={i === chosen}
						role="option"
						aria-selected={i === chosen}
						onmousedown={(e) => {
							e.preventDefault();
							accept(project);
						}}
					>
						<span class="dot" style="background: {colorVar(project.color)}" aria-hidden="true"
						></span>
						#{project.name}
					</button>
				</li>
			{/each}
		</ul>
	{/if}

	<!-- What the text understands, said where it is being typed. The same idea as
	     the line under the quick-add box: a note is the other place in this program
	     where what you type is read for meaning. -->
	<p class="legend">
		{#each SYNTAX as item}
			<span><kbd>{item.mark}</kbd> {t(item.what)}</span>
		{/each}
	</p>
</div>

<style>
	.wrap {
		display: flex;
		flex-direction: column;
		min-height: 0;
		flex: 1;
	}

	.toolbar {
		display: flex;
		align-items: center;
		gap: var(--s1);
		padding: 0 0 var(--s2);
		border-bottom: 1px solid var(--line);
	}

	.toolbar button {
		min-width: 28px;
		height: 28px;
		border-radius: var(--radius-sm);
		color: var(--ink-muted);
		font-size: var(--text-sm);
	}

	.toolbar button:hover {
		background: var(--surface);
		color: var(--ink);
	}

	.toolbar button.on {
		background: var(--surface-raised);
		color: var(--ink);
	}

	.sep {
		width: 1px;
		height: 18px;
		background: var(--line);
		margin: 0 var(--s1);
	}

	.styles {
		position: relative;
	}

	.aa {
		font-weight: 560;
	}

	.menu {
		position: absolute;
		top: calc(100% + var(--s1));
		left: 0;
		z-index: 20;
		min-width: 210px;
		padding: var(--s1);
		background: var(--surface-raised);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		box-shadow: 0 8px 24px rgb(0 0 0 / 0.25);
		display: flex;
		flex-direction: column;
	}

	.menu button {
		width: 100%;
		text-align: left;
		padding: var(--s1) var(--s2);
		height: auto;
		min-height: 30px;
	}

	.menu hr {
		border: 0;
		border-top: 1px solid var(--line);
		margin: var(--s1) 0;
	}

	/* The menu shows each style in the style it applies, the way Apple Notes does.
	   Reading the word "Overskrift" set as one is faster than reading a label. */
	.menu .title {
		font-size: 1.35rem;
		font-weight: 700;
		color: var(--ink);
	}

	.menu .heading {
		font-size: 1.1rem;
		font-weight: 640;
		color: var(--ink);
	}

	.menu .subheading {
		font-weight: 620;
		color: var(--ink);
	}

	.menu .mono {
		font-family: var(--mono, ui-monospace, monospace);
	}

	.menu .quote {
		border-left: 2px solid var(--line-strong);
		padding-left: var(--s2);
		color: var(--ink-muted);
	}

	.suggestions {
		position: absolute;
		z-index: 30;
		list-style: none;
		margin: 0;
		padding: var(--s1);
		min-width: 200px;
		background: var(--surface-raised);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		box-shadow: 0 8px 24px rgb(0 0 0 / 0.25);
	}

	.suggestions button {
		display: flex;
		align-items: center;
		gap: var(--s2);
		width: 100%;
		text-align: left;
		padding: var(--s1) var(--s2);
		border-radius: var(--radius-sm);
		color: var(--ink-muted);
		font-size: var(--text-sm);
	}

	.suggestions button.on {
		background: var(--surface);
		color: var(--ink);
	}

	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: var(--s1) var(--s3);
		padding-top: var(--s2);
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.legend kbd {
		font-family: var(--mono, ui-monospace, monospace);
		color: var(--ink-muted);
	}

	.wrap {
		position: relative;
	}

	.page {
		/* Et token eller en URL er ét ord, der er bredere end skærmen. Uden det her
		   får siden en vandret rullebjælke, og teksten forsvinder ud til højre. */
		overflow-wrap: anywhere;
		flex: 1;
		min-height: 50vh;
		padding: var(--s3) 0;
		outline: none;
		line-height: 1.55;
		overflow-y: auto;
	}

	/* The document styles. These are the whole of "it should look like Apple Notes":
	   a title that is a title, and body text that is comfortable to read for a page
	   at a time. */
	.page :global(h1) {
		font-size: 1.5rem;
		font-weight: 700;
		margin: 0 0 var(--s2);
		line-height: 1.25;
	}

	.page :global(h2) {
		font-size: 1.2rem;
		font-weight: 640;
		margin: var(--s3) 0 var(--s1);
	}

	.page :global(h3) {
		font-size: 1rem;
		font-weight: 620;
		margin: var(--s3) 0 var(--s1);
	}

	.page :global(p) {
		margin: 0 0 var(--s2);
	}

	.page :global(ul),
	.page :global(ol) {
		margin: 0 0 var(--s2);
		padding-left: var(--s4);
	}

	.page :global(li) {
		margin-bottom: var(--s1);
	}

	.page :global(blockquote) {
		margin: 0 0 var(--s2);
		padding-left: var(--s3);
		border-left: 2px solid var(--line-strong);
		color: var(--ink-muted);
	}

	/* En kodeblok ser ud som en terminal, fordi det er hvad den er.
	   Monospace er altid kode i praksis — Apple Noters "Monotype" er stort set kun
	   brugt til kommandoer og udskrifter — og en mørk flade siger det på afstand,
	   uden at man skal læse et ord. Farverne er de samme i lys og mørk tilstand:
	   en terminal er sort, også midt på dagen. */
	.page :global(pre) {
		margin: 0 0 var(--s3);
		padding: var(--s3);
		border-radius: var(--radius);
		background: #14181b;
		color: #d7dde3;
		font-family: var(--mono, ui-monospace, monospace);
		font-size: 0.8125rem;
		line-height: 1.5;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		position: relative;
	}

	/* Sproget i hjørnet, når blokken siger hvad den er. */
	.page :global(pre[data-lang]:not([data-lang=''])::after) {
		content: attr(data-lang);
		position: absolute;
		top: 4px;
		right: 8px;
		font-size: 0.625rem;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: #5a656e;
	}

	.page :global(pre code) {
		font: inherit;
		color: inherit;
	}

	.page :global(.tok-comment) {
		color: #6b7a85;
		font-style: italic;
	}

	.page :global(.tok-string) {
		color: #9ecb8a;
	}

	.page :global(.tok-number) {
		color: #d9a25f;
	}

	.page :global(.tok-keyword) {
		color: #7fb3e8;
	}

	.page :global(.tok-key) {
		color: #7fb3e8;
	}

	.page :global(.tok-bool) {
		color: #c48fd6;
	}

	.page :global(.tok-variable) {
		color: #d9a25f;
	}

	.page :global(.tok-flag) {
		color: #c48fd6;
	}

	.page :global(.tok-punct) {
		color: #8a949c;
	}

	/* Prompten i en terminaludskrift: det første øjet finder, når man leder efter
	   hvor en kommando begynder. */
	.page :global(.tok-prompt) {
		color: #6fbf8f;
		font-weight: 600;
	}

	.page :global(code) {
		font-family: var(--mono, ui-monospace, monospace);
		color: var(--accent);
	}
</style>
