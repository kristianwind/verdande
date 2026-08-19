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
	import { t } from '$lib/i18n.svelte.js';

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
		editor.innerHTML = markdownToHtml(note.body) || '<p><br></p>';
		loadedId = note.id;
		active = {};
	});

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

	function changed() {
		if (!editor) return;
		onchange?.(htmlToMarkdown(editor));
	}

	function onkeydown(event) {
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
		onblur={onsave}
		onkeyup={readState}
		onmouseup={readState}
		{onkeydown}
		{onpaste}
	></div>
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

	.page {
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

	.page :global(pre) {
		margin: 0 0 var(--s2);
		font-family: var(--mono, ui-monospace, monospace);
		white-space: pre-wrap;
	}

	.page :global(code) {
		font-family: var(--mono, ui-monospace, monospace);
		color: var(--accent);
	}
</style>
