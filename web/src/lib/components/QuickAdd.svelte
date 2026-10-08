<script>
	/**
	 * The quick-add box.
	 *
	 * What makes it worth its own component is the highlighting: as you type, the
	 * parts the parser understood are tinted underneath the text. That is the only
	 * way "i morgen kl 10 p1" stops feeling like guesswork — you can see it being
	 * read before you commit to it.
	 *
	 * The tinting is a mirrored layer sitting exactly behind a transparent input.
	 * A contenteditable would let us style the text directly and would also break
	 * autocorrect, IME input and undo, which is a bad trade for a box people type
	 * one line into.
	 */
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';
	import { readList } from '$lib/pastelist.js';
	let {
		projectId = undefined,
		onadded = undefined,
		autofocus = false,
		/**
		 * Whether this box carries the `data-quickadd` marker.
		 *
		 * The layout's type-anywhere shortcut finds the field with
		 * `document.querySelector('[data-quickadd]')`, which returns the FIRST match
		 * in the document. So a second box on the page — the dictate sheet's — has
		 * to stay unmarked, or the shortcut starts typing into a box nobody can see.
		 */
		marked = true,
		/**
		 * The field's accessible name.
		 *
		 * A prop because the dictate sheet puts a second one of these on the page,
		 * and two fields answering to "Ny opgave" is a screen reader reading the
		 * same name twice with no way to tell which is which. They are different
		 * affordances — one is the box on the page, the other is the thing the
		 * round button opens — so they say different names.
		 */
		label = undefined
	} = $props();

	let fieldLabel = $derived(label ?? t('task.new'));

	let text = $state('');
	let spans = $state([]);
	let submitting = $state(false);
	let input;
	let mirror;
	let focused = $state(false);

	/**
	 * Keeps the tint under the right characters when the line is longer than the
	 * field.
	 *
	 * The input scrolls horizontally and the mirror has to scroll with it. Done on
	 * both `scroll` and `input` because typing at the end of a long line moves the
	 * caret — and therefore the scroll — without ever firing a scroll event in some
	 * browsers.
	 */
	function syncScroll() {
		if (mirror && input) mirror.scrollLeft = input.scrollLeft;
	}

	// Kept beside the parser's own vocabulary on purpose: a legend that promises
	// something internal/quickadd cannot read is worse than no legend. These four
	// are what Parse recognises as marks; dates it reads as prose, which is why the
	// last entry is an example rather than a symbol.
	let syntax = $derived([
		{ mark: '#', what: t('task.syntaxProject') },
		{ mark: '/', what: t('task.syntaxSection') },
		{ mark: '@', what: t('task.syntaxLabel') },
		{ mark: 'p1', what: t('task.syntaxPriority') },
		{ mark: t('task.syntaxDateMark'), what: t('task.syntaxDate') }
	]);

	let previewController = null;
	let previewTimer = null;

	/**
	 * Parsing runs on the server, so it is debounced and each request cancels the
	 * one before it. Without the cancel, a fast typist has six parses in flight and
	 * the highlighting flickers between whichever happens to land last.
	 */
	function schedulePreview(value) {
		clearTimeout(previewTimer);
		previewController?.abort();

		if (!value.trim()) {
			spans = [];
			return;
		}
		previewTimer = setTimeout(async () => {
			previewController = new AbortController();
			try {
				const result = await api.quickAddPreview(value, previewController.signal);
				spans = result.spans ?? [];
			} catch {
				// An aborted or failed preview just means no highlighting; the text
				// is still perfectly submittable.
			}
		}, 120);
	}

	$effect(() => {
		schedulePreview(text);
	});

	$effect(() => {
		if (autofocus && input) input.focus();
	});

	/**
	 * Focus the field from outside, synchronously.
	 *
	 * This exists because of iOS and nothing else. Safari opens the on-screen
	 * keyboard only when `focus()` is called INSIDE the user gesture that asked for
	 * it — so the `autofocus` effect above, which runs after the click handler has
	 * returned, gives the field a caret and no keyboard. For the dictate button that
	 * is the whole feature failing quietly: the sheet opens, nothing to dictate
	 * into, and there is no error anywhere.
	 */
	export function focusField() {
		input?.focus();
	}

	/** Splits the raw text into highlighted and plain runs, by byte offset. */
	let segments = $derived.by(() => {
		if (!spans.length) return [{ text, kind: null }];

		// The server counts bytes; JavaScript counts UTF-16 units. For "køb" those
		// disagree, and using one for the other paints the highlight over the wrong
		// characters — so the string is converted and sliced as bytes.
		const encoder = new TextEncoder();
		const decoder = new TextDecoder();
		const bytes = encoder.encode(text);

		const out = [];
		let cursor = 0;
		for (const span of [...spans].sort((a, b) => a.start - b.start)) {
			if (span.start > cursor) {
				out.push({ text: decoder.decode(bytes.slice(cursor, span.start)), kind: null });
			}
			out.push({ text: decoder.decode(bytes.slice(span.start, span.end)), kind: span.kind });
			cursor = span.end;
		}
		if (cursor < bytes.length) {
			out.push({ text: decoder.decode(bytes.slice(cursor)), kind: null });
		}
		return out;
	});

	/**
	 * En indsat liste bliver til opgaver frem for til én opgave med det hele i.
	 *
	 * Browseren smider linjeskiftene væk, når man indsætter i et `<input>`, så uden
	 * det her blev en kopieret liste til én opgave med punkterne mast sammen. En
	 * enkelt linje røres ikke — det er almindelig indsætning, og den skal opføre sig
	 * som altid.
	 *
	 * Vist før den oprettes. Fyrre opgaver fra et fejlklik er ikke noget, man skal
	 * opdage bagefter, og listen er også svaret på "forstod den indrykningen" —
	 * stillet mens der stadig er en Fortryd-knap.
	 */
	let pending = $state([]);

	function onpaste(event) {
		const pasted = event.clipboardData?.getData('text/plain') ?? '';
		if (!pasted.includes('\n')) return;

		const rows = readList(pasted);
		if (rows.length < 2) return;

		event.preventDefault();
		pending = rows;
	}

	async function createPending() {
		if (submitting) return;
		submitting = true;

		// Den seneste opgave på hvert niveau, så en indrykket linje havner under den
		// linje, den stod under — og ikke under den første, den bedste.
		const parents = [];
		let made = 0;
		for (const row of pending) {
			const parentId = row.depth > 0 ? parents[row.depth - 1] : undefined;
			const task = await app.quickAdd(row.text, projectId, undefined, parentId);
			// Stopper ved den første, der ikke kunne laves. At køre videre ville lægge
			// resten af listen ind uden den, de hang under, og det er værre end at
			// standse et sted, man kan se.
			if (!task) break;
			parents[row.depth] = task.id;
			parents.length = row.depth + 1;
			if (row.done) await app.complete(task.id);
			onadded?.(task);
			made++;
		}

		submitting = false;
		// Det, der ikke nåede at blive lavet, bliver stående, så man kan se hvor langt
		// den kom og prøve resten igen.
		pending = pending.slice(made);
		input?.focus();
	}

	async function submit(event) {
		event?.preventDefault();
		const value = text.trim();
		if (!value || submitting) return;

		submitting = true;
		// Cleared before the response: the box has to be ready for the next thought
		// immediately, and a failure puts the text back rather than blocking on it.
		text = '';
		spans = [];

		const task = await app.quickAdd(value, projectId);
		submitting = false;
		if (task) onadded?.(task);
		else text = value;

		input?.focus();
	}

	function onkeydown(event) {
		if (event.key === 'Escape') {
			text = '';
			spans = [];
			input?.blur();
			return;
		}
		// Submitting on Enter explicitly rather than relying on the browser's
		// implicit form submission: that only fires when the form has a submit
		// button, and this one is hidden while the field is empty. Tying the
		// primary interaction of the app to a conditional element is not a
		// dependency worth having.
		if (event.key === 'Enter' && !event.isComposing) {
			event.preventDefault();
			submit();
		}
	}
</script>

<form class="quickadd" onsubmit={submit}>
	<span class="plus" aria-hidden="true">+</span>

	<div class="field">
		<!-- The mirror sits behind the input and must match it exactly: same font,
		     same padding, same wrapping. Any difference shows up as highlighting
		     that drifts out of alignment as the line grows. -->
		<div class="mirror" aria-hidden="true" bind:this={mirror}>
			{#each segments as segment}
				{#if segment.kind}
					<mark data-kind={segment.kind}>{segment.text}</mark>
				{:else}
					<span>{segment.text}</span>
				{/if}
			{/each}
		</div>

		<input
			bind:this={input}
			bind:value={text}
			{onkeydown}
			onscroll={syncScroll}
			oninput={syncScroll}
			onfocus={() => (focused = true)}
			onblur={() => (focused = false)}
			type="text"
			data-quickadd={marked ? '' : undefined}
			placeholder={t('task.placeholder')}
			aria-label={fieldLabel}
			autocomplete="off"
			spellcheck="false"
			{onpaste}
		/>
	</div>

	{#if text.trim() && !pending.length}
		<button type="submit" class="submit" disabled={submitting}>{t('task.add')}</button>
	{/if}

	{#if pending.length}
		<div class="pasted" role="group" aria-label={t('task.pastedTitle')}>
			<ul>
				{#each pending as row, i (i)}
					<li style="--depth: {row.depth}" class:done={row.done}>{row.text}</li>
				{/each}
			</ul>
			<div class="actions">
				<button type="button" class="submit" disabled={submitting} onclick={createPending}>
					{t('task.pastedAdd', { n: String(pending.length) })}
				</button>
				<button type="button" class="cancel" onclick={() => (pending = [])}>
					{t('task.pastedCancel')}
				</button>
			</div>
		</div>
	{/if}

	<!-- What the parser understands, spelled out while somebody is typing into it.
	     It was in the placeholder before, which is the one place a hint cannot be:
	     the placeholder disappears at the first keystroke, so the help was gone
	     exactly when it started being useful.

	     Floated rather than in the flow. In the flow it pushed the whole list down
	     the moment the field took focus, and a click aimed at the first task landed
	     on the second — twelve tests caught it, which is twelve more than the eye
	     would have. -->
	{#if focused}
		<p class="syntax">
			{#each syntax as item}
				<span><kbd>{item.mark}</kbd> {item.what}</span>
			{/each}
		</p>
	{/if}
</form>

<style>
	/* Den indsatte liste, før den bliver til opgaver. Ligger i flowet og ikke
	   svævende: den skal kunne klikkes på, og den er noget, man læser færdig frem
	   for et vink, der forsvinder. */
	.pasted {
		grid-column: 1 / -1;
		margin-top: var(--s2);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		background: var(--surface);
		padding: var(--s2);
	}

	.pasted ul {
		list-style: none;
		margin: 0 0 var(--s2);
		padding: 0;
		max-height: 40vh;
		overflow-y: auto;
	}

	.pasted li {
		/* Indrykningen er hele pointen med visningen: den viser, hvad der bliver en
		   underopgave, mens der stadig er en vej tilbage. */
		padding: 2px 0 2px calc(var(--depth) * var(--s4));
		font-size: var(--text-sm);
		color: var(--ink);
	}

	.pasted li.done {
		text-decoration: line-through;
		color: var(--ink-muted);
	}

	.pasted .actions {
		display: flex;
		gap: var(--s2);
		align-items: center;
	}

	.cancel {
		border: none;
		background: none;
		padding: 0;
		font: inherit;
		color: var(--ink-muted);
		cursor: pointer;
	}
	.cancel:hover {
		text-decoration: underline;
	}

	/* Sits under the field it explains, in the same rhythm as a hint anywhere else. */
	.syntax {
		/* Nothing to click, and it floats over the first task in the list — which it
		   promptly swallowed the clicks of. Legends are for reading. */
		pointer-events: none;
		position: absolute;
		top: 100%;
		left: 0;
		right: 0;
		z-index: 2;
		background: var(--surface);
		display: flex;
		flex-wrap: wrap;
		gap: var(--s1) var(--s3);
		padding: var(--s1) 0 0 var(--s4);
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.syntax kbd {
		font-family: var(--mono, ui-monospace, monospace);
		color: var(--ink-muted);
	}

	.quickadd {
		position: relative;
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3) var(--s2);
		border-bottom: 1px solid var(--line);
		/* Luft under stregen.
		 *
		 * Stregen skiller "skriv en ny" fra "det, der allerede er", og uden afstand
		 * sad den første sektion klods op ad den — så den læste som en kant om
		 * sektionen frem for som en afslutning på feltet over. En streg skal have
		 * plads på begge sider for at kunne ses som en streg. */
		margin-bottom: var(--s4);
	}

	.plus {
		flex: none;
		width: 20px;
		height: 20px;
		display: grid;
		place-items: center;
		color: var(--ink-faint);
		font-size: var(--text-lg);
		line-height: 1;
	}

	.quickadd:focus-within .plus {
		color: var(--accent);
	}

	.field {
		position: relative;
		flex: 1;
		min-width: 0;
	}

	/* Both layers share every metric that affects text layout. */
	.mirror,
	.field input {
		font: inherit;
		font-size: var(--text-base);
		line-height: 1.5;
		letter-spacing: normal;
		padding: 0;
		border: 0;
		/* `pre`, not `pre-wrap`, and that is the whole alignment.
		   
		   An <input type="text"> is single-line by specification and cannot wrap
		   whatever CSS says. So a wrapping mirror behind a non-wrapping input does
		   not drift a little — it puts the tint on a second line that the input does
		   not have, and the highlight ends up under empty space. Rare on a wide
		   desktop field, constant in the dictate sheet on a 390px phone, where a
		   spoken sentence overflows immediately. */
		white-space: pre;
		overflow-wrap: normal;
	}

	.mirror {
		position: absolute;
		inset: 0;
		pointer-events: none;
		color: transparent;
		/* Scrolled in step with the input by hand — see `syncScroll`. The overflow
		   has to be hidden rather than visible, or the long tail of a line paints
		   out past the field's edge. */
		overflow: hidden;
	}

	/* The tint is behind the glyphs, not on them: colouring the text itself would
	   fight the reading of the sentence, while a wash behind it reads as "this bit
	   was understood" without changing the words. */
	.mirror mark {
		background: var(--accent-sunken);
		color: transparent;
		border-radius: var(--radius-sm);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent) 25%, transparent);
	}

	.mirror mark[data-kind='priority'] {
		background: color-mix(in srgb, var(--p1) 18%, transparent);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--p1) 30%, transparent);
	}

	.mirror mark[data-kind='project'] {
		background: color-mix(in srgb, var(--p3) 18%, transparent);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--p3) 30%, transparent);
	}

	.mirror mark[data-kind='label'] {
		background: color-mix(in srgb, var(--p2) 18%, transparent);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--p2) 30%, transparent);
	}

	.field input {
		position: relative;
		width: 100%;
		background: transparent;
		color: var(--ink);
		outline: none;
	}

	.field input::placeholder {
		color: var(--ink-faint);
	}

	.submit {
		flex: none;
		background: var(--accent);
		color: var(--accent-ink);
		font-size: var(--text-sm);
		font-weight: 550;
		padding: var(--s1) var(--s3);
		border-radius: var(--radius);
		transition: background var(--fast) var(--ease);
	}

	.submit:hover:not(:disabled) {
		background: var(--accent-hover);
	}

	.submit:disabled {
		opacity: 0.5;
		cursor: default;
	}
</style>
