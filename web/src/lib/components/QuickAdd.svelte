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
	let { projectId = undefined, onadded = undefined, autofocus = false } = $props();

	let text = $state('');
	let spans = $state([]);
	let submitting = $state(false);
	let input;

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
		<div class="mirror" aria-hidden="true">
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
			type="text"
			placeholder={t('task.placeholder')}
			aria-label={t('task.new')}
			autocomplete="off"
			spellcheck="false"
		/>
	</div>

	{#if text.trim()}
		<button type="submit" class="submit" disabled={submitting}>{t('task.add')}</button>
	{/if}
</form>

<style>
	.quickadd {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3) var(--s2);
		border-bottom: 1px solid var(--line);
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
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	.mirror {
		position: absolute;
		inset: 0;
		pointer-events: none;
		color: transparent;
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
