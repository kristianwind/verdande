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
	import { api, humanMessage } from '$lib/api.js';
	import { colorVar } from '$lib/colors.js';

	let { note, onchange, onsave } = $props();

	let editor;
	// Forslagslisten, så tastaturvalget kan rulle den frem.
	let list;
	let stylesOpen = $state(false);
	// Which marks are on where the caret is, so the toolbar shows state rather than
	// only offering actions.
	let active = $state({});

	// Loaded when the note changes, never while it is being typed in: writing to
	// innerHTML puts the caret back at the start, which mid-sentence is unusable.
	let loadedId = $state(null);
	$effect(() => {
		if (!editor || !note || note.id === loadedId) return;

		// Sat *før* tegningen, ikke efter.
		//
		// Kaster noget herunder, er noten stadig den, der er forsøgt indlæst — og
		// uden det her ville effekten prøve igen ved hver eneste ændring bagefter og
		// kaste hver gang.
		loadedId = note.id;
		active = {};
		// Kildevisningen hører til den note, den blev slået til på. Skifter man note,
		// er det den rige flade, man skal møde — ellers står den næste note som kode,
		// uden at nogen har bedt om det.
		asSource = false;

		// Én note, der ikke kan tegnes, må ikke tage ruden med sig.
		//
		// Det var `note.body.trim()` uden værn, og `body` er en streng fra serveren
		// hver eneste gang — indtil den ikke er. Så kaster den her inde i et
		// `$effect`, og en effekt, der kaster, tager komponentens opdateringer med
		// sig: den forrige note bliver stående, og *ingen* note kan åbnes bagefter.
		// Klikket kan ikke engang lande. Meldt ind som "jeg kan ikke skifte mellem
		// noter, uanset hvilken jeg trykker på", hvilket er præcis, hvad det ser ud
		// som fra den anden side.
		//
		// Fejlen står i konsollen frem for i en toast: den hører til den, der kan
		// rette den, og en note, der viser sin egen tekst uden formatering, siger
		// allerede til den, der læser den, at noget er galt.
		//
		// `textContent` og ikke `innerHTML` i faldbakken — teksten er ikke tegnet, og
		// at lægge den ind som markup ville være at køre det, tegningen lige nægtede.
		// Gemningen er ikke i fare: den skriver `draft`, som kommer fra svaret og
		// ikke fra det, der står i editoren, indtil nogen taster i den.
		const body = note.body ?? '';
		try {
			// An empty note opens on its title. That is what Apple Notes does, and it
			// is also what the list needs: the first line becomes the name of the
			// note, so a note whose first line is body text is a note called by its
			// first sentence.
			//
			// Tested on the text and not on the rendered result: an empty body renders
			// as one empty paragraph, not as nothing, so a falsy check here never
			// fired.
			editor.innerHTML = body.trim() ? markdownToHtml(body) : '<h1><br></h1>';
			colourCode();
		} catch (e) {
			editor.textContent = body;
			console.error('verdande: noten kunne ikke tegnes', note.id, e);
		}
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
	/**
	 * En kopiér-knap på hver kodeblok.
	 *
	 * Lagt oven på blokken frem for skrevet ind i den: alt inde i `.page` er tekst,
	 * der bliver til Markdown, når noten gemmes, så en knap derinde ville ende i
	 * filen. Den her er `contenteditable="false"` og hænger uden for tekstflowet —
	 * den findes for øjet og musen, ikke for dokumentet.
	 *
	 * `navigator.clipboard` fejler, når siden ikke er sikker eller ikke har fokus,
	 * og en knap, der siger "Kopieret" uden at have gjort det, er værre end ingen
	 * knap. Derfor siges det kun, når skrivningen faktisk lykkedes.
	 */
	function addCopyButtons() {
		if (!editor) return;
		for (const pre of editor.querySelectorAll('pre')) {
			if (pre.querySelector('.copy')) continue;
			const btn = document.createElement('button');
			btn.className = 'copy';
			btn.type = 'button';
			btn.contentEditable = 'false';
			btn.textContent = t('notes.copyCode');
			btn.addEventListener('mousedown', (e) => e.preventDefault());
			btn.addEventListener('click', async (e) => {
				e.preventDefault();
				e.stopPropagation();
				const text = codeTextOf(pre);
				try {
					await navigator.clipboard.writeText(text.replace(/\n$/, ''));
					btn.textContent = t('notes.copied');
					setTimeout(() => (btn.textContent = t('notes.copyCode')), 1200);
				} catch {
					app.toast(t('notes.copyFailed'));
				}
			});
			pre.appendChild(btn);
		}
	}

	/**
	 * Teksten i en kodeblok, uden knapper og andet, editoren har lagt oven på den.
	 *
	 * Ét sted, fordi to steder skal bruge det samme svar: farvelægningen, som
	 * skriver resultatet tilbage i blokken, og oversættelsen til Markdown, som
	 * skriver det i filen. Var de to uenige, ville forskellen ende i noten.
	 */
	function codeTextOf(pre) {
		const from = pre.querySelector('code') ?? pre;
		return [...from.childNodes]
			.filter((n) => n.nodeType !== Node.ELEMENT_NODE || !n.classList?.contains('copy'))
			.map((n) => n.textContent ?? '')
			.join('');
	}

	function colourCode() {
		if (!editor) return;
		const inside = currentBlock();
		for (const pre of editor.querySelectorAll('pre')) {
			if (pre === inside || pre.contains(inside)) continue;
			// Blokkens tekst uden det, vi selv har lagt oven på den.
			//
			// `pre.textContent` tog kopiér-knappens eget ord med, og linjen under
			// maler resultatet tilbage ind i blokken — så et klik på "Kopiér" skrev
			// "Kopieret" ind i koden, og den næste gemning lagde det i filen. Knappen
			// spiste den kode, den var sat der for at kopiere.
			const code = codeTextOf(pre);
			const lang = pre.getAttribute('data-lang') || guessLanguage(code);
			if (lang) pre.setAttribute('data-lang', lang);
			const painted = highlight(code, lang);
			// Kun hvis der er noget at ændre: at skrive innerHTML uændret ville
			// stadig rive noden op og lægge den tilbage, og det ses som et blink.
			const target = pre.querySelector('code') ?? pre;
			if (target.innerHTML !== painted) target.innerHTML = painted;
		}
		addCopyButtons();
	}

	function changed() {
		if (!editor) return;
		onchange?.(htmlToMarkdown(editor));
		readSuggestions();
	}

	// --- kilden ----------------------------------------------------------------------

	/**
	 * Noten som den Markdown, den er.
	 *
	 * Fladen er rig tekst, fordi det er den, folk kan. Men filen under er Markdown,
	 * og der er to ting, den rige flade ikke kan: tage imod Markdown, nogen har
	 * skrevet et andet sted — indsat i den rige flade er `## Overskrift` fem tegn og
	 * ikke en overskrift — og vise, hvad der faktisk bliver gemt. Begge dele er det
	 * samme svar: lad folk se kilden.
	 *
	 * Ikke en tredje tilstand at holde styr på: teksten går gennem de samme to
	 * funktioner som altid, kun den anden vej. Det, man ser i kildefeltet, er ord for
	 * ord det, der ligger i databasen.
	 */
	let asSource = $state(false);
	let source = $state('');

	function toggleSource() {
		if (!asSource) {
			source = htmlToMarkdown(editor);
			asSource = true;
			return;
		}
		asSource = false;
		onchange?.(source);
		// Fyldt fra kilden, ikke fra `note.body`.
		//
		// Noten i lageret er den sidst gemte, og gemningen venter på sin pause — så
		// den vej ville kaste det, der lige er skrevet i kildefeltet, væk. `loadedId`
		// røres ikke: $effect ser stadig en note, den har indlæst, og skriver ikke
		// hen over det her.
		queueMicrotask(() => {
			if (!editor) return;
			editor.innerHTML = source.trim() ? markdownToHtml(source) : '<h1><br></h1>';
			colourCode();
		});
	}

	/** Kilden skrives direkte igennem: det er filen, man redigerer. */
	function sourceChanged(event) {
		source = event.currentTarget.value;
		onchange?.(source);
	}

	// --- suggesting a project ------------------------------------------------------

	let suggestions = $state([]);
	let chosen = $state(0);
	// Hvor forslagslisten skal stå, og wrappen den måles imod.
	let menuAt = $state(null);
	let wrap;

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

	/**
	 * Hvor `#` står, målt i wrappens koordinater.
	 *
	 * Målt på selve tegnet og ikke på markøren: et sammenfaldet område har ingen
	 * kasse på en tom linje, mens `#term` altid har en, fordi der står noget i den.
	 *
	 * Klemt inden for ruden, så en note skrevet helt ude i højre side ikke sender
	 * menuen ud over kanten, og vendt op over linjen, når der ikke er plads under.
	 */
	function tagPosition(partial) {
		if (!wrap) return null;
		const range = document.createRange();
		range.setStart(partial.node, Math.max(0, partial.start - 1));
		range.setEnd(partial.node, partial.start + partial.term.length);
		const at = range.getBoundingClientRect();
		const box = wrap.getBoundingClientRect();
		if (!at.width && !at.height) return null;

		const MENU = { w: 210, h: 240 };
		const below = at.bottom - box.top + 6;
		const flip = at.bottom + MENU.h > box.bottom && at.top - box.top > MENU.h;
		return {
			top: flip ? at.top - box.top - MENU.h - 6 : below,
			left: Math.max(0, Math.min(at.left - box.left, box.width - MENU.w))
		};
	}

	function readSuggestions() {
		const partial = partialTag();
		if (!partial) {
			suggestions = [];
			return;
		}
		const term = partial.term.toLowerCase();

		// Målt når menuen åbner, ikke ved hvert tastetryk. Den skal stå ved det `#`,
		// man skriver — den stod før øverst i ruden, hvilket i en lang note er et
		// helt andet sted end det, øjet er — men den må heller ikke rykke sig et par
		// pixels for hvert bogstav, mens listen snævres ind.
		if (!suggestions.length) menuAt = tagPosition(partial);

		// Alle der passer, ikke de seks første.
		//
		// Loftet på seks var sat, dengang listen ikke kunne rulle, og det gjorde
		// noget værre end at skjule: med tyve projekter og et blot tastet `#` viste
		// den seks vilkårlige og så færdig ud. Man kunne ikke se, at der manglede
		// noget — kun at ens eget projekt ikke var der.
		//
		// Rækkefølgen bærer nu det, loftet gjorde forkert: det, der begynder med
		// det tastede, står øverst, resten alfabetisk. Skriver man "ga", er
		// GarageRisteriet den første, ikke den, der tilfældigvis lå først.
		const matches = app.projects.filter(
			(p) => !p.is_inbox && p.name.toLowerCase().includes(term)
		);
		matches.sort((a, b) => {
			const an = a.name.toLowerCase();
			const bn = b.name.toLowerCase();
			const ap = an.startsWith(term);
			const bp = bn.startsWith(term);
			if (ap !== bp) return ap ? -1 : 1;
			return an.localeCompare(bn, 'da');
		});
		suggestions = matches;
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

	/**
	 * Ruller det valgte forslag frem, når listen er længere end sin egen kasse.
	 *
	 * Efter opdateringen, ikke før: `chosen` er netop sat, og rækken bærer klassen
	 * først når Svelte har skrevet den ud. Uden ventetiden ruller den til den
	 * forrige.
	 */
	function keepChosenInView() {
		requestAnimationFrame(() => {
			list?.querySelector('button.on')?.scrollIntoView({ block: 'nearest' });
		});
	}

	function onkeydown(event) {
		// The suggestion list owns the arrows and return while it is open, the way
		// every other completion does. Escape closes it without choosing.
		if (suggestions.length) {
			if (event.key === 'ArrowDown') {
				event.preventDefault();
				chosen = (chosen + 1) % suggestions.length;
				keepChosenInView();
				return;
			}
			if (event.key === 'ArrowUp') {
				event.preventDefault();
				chosen = (chosen - 1 + suggestions.length) % suggestions.length;
				keepChosenInView();
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
	/**
	 * Filer ind i noten — indsat eller trukket.
	 *
	 * Importen kunne tage billeder med, og intet andet kunne. At indsætte et
	 * skærmbillede i en note er det mest almindelige, der findes i et noteprogram,
	 * og det gjorde ingenting overhovedet: indsætningen tog kun `text/plain` og
	 * smed resten væk, og der var slet ingen håndtering af et træk.
	 *
	 * Filen lægges op og skrives ind som `![](…)`. Ikke som en data-URL: en note
	 * er Markdown i en database, og et fotografi fra en telefon som base64 midt i
	 * teksten ville gøre noten til fire megabyte tekst, som skal læses hver gang
	 * listen tegnes.
	 *
	 * En note, der ikke er gemt endnu, kan ikke bære en fil — den har intet id at
	 * hænge den på. Derfor gemmes den først, og det siges ikke til nogen, fordi
	 * det er en teknisk detalje og ikke noget, brugeren har bedt om at vide.
	 */
	async function insertFiles(files) {
		const list = [...files].filter((f) => f && f.size > 0);
		if (!list.length || !note?.id) return false;

		uploading = true;
		try {
			for (const file of list) {
				const saved = await api.uploadNoteFile(note.id, file);
				// execCommand frem for at bygge noden selv: det er det, der lægger
				// handlingen i browserens egen fortryd-historik, så ⌘Z tager billedet
				// ud igen ligesom det tager et ord ud.
				editor?.focus();
				document.execCommand('insertHTML', false,
					`<img src="${saved.url}" alt="">`);
			}
			changed();
			onsave?.();
			return true;
		} catch (e) {
			app.toast(humanMessage(e));
			return false;
		} finally {
			uploading = false;
		}
	}

	let uploading = $state(false);

	function onpaste(event) {
		// Filer først. Et skærmbillede i udklipsholderen kommer med som en fil og
		// som ingenting andet, så hvis der er en, er det den, der skal ind.
		const files = event.clipboardData?.files ?? [];
		if (files.length) {
			event.preventDefault();
			insertFiles(files);
			return;
		}

		event.preventDefault();
		const text = event.clipboardData?.getData('text/plain') ?? '';
		document.execCommand('insertText', false, text);
		changed();
	}

	/**
	 * Trukket ind udefra.
	 *
	 * `preventDefault` på begge: uden den på dragover kommer der ingen drop, og
	 * uden den på drop åbner browseren filen i stedet — man mister det, man var i
	 * gang med at skrive, til en fane med et billede i.
	 *
	 * Kun filer. Et træk inde fra appen selv — en opgave, en note — bærer vores
	 * egen nyttelast og skal falde igennem til den, der lyttede efter den.
	 */
	function ondragover(event) {
		if (!event.dataTransfer?.types?.includes('Files')) return;
		event.preventDefault();
		dropping = true;
	}

	function ondrop(event) {
		const files = event.dataTransfer?.files ?? [];
		dropping = false;
		if (!files.length) return;
		event.preventDefault();
		insertFiles(files);
	}

	let dropping = $state(false);
</script>

<div class="wrap" bind:this={wrap}>
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

		<span class="sep" aria-hidden="true"></span>

		<!-- Kilden. Til højre for de andre, fordi den ikke er et format men en
		     anden måde at se det hele på. -->
		<button
			class="src"
			class:on={asSource}
			onclick={toggleSource}
			aria-pressed={asSource}
			aria-label={t('notes.showSource')}
			title={t('notes.showSource')}>&lt;/&gt;</button
		>
	</div>

	{#if asSource}
		<!-- Et almindeligt tekstfelt, fordi det er almindelig tekst. Ingen
		     contenteditable, ingen execCommand: her er der intet at formatere. -->
		<textarea
			class="page source"
			aria-label={t('notes.source')}
			spellcheck="false"
			value={source}
			oninput={sourceChanged}
			onblur={() => onsave?.()}
		></textarea>
	{/if}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		class="page"
		class:hidden={asSource}
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
		{ondragover}
		{ondrop}
		ondragleave={() => (dropping = false)}
		class:dropping
	></div>

	{#if suggestions.length}
		<!-- Ved det `#`, der bliver skrevet. Målt én gang, da menuen åbnede, så den
		     står stille, mens listen snævres ind. -->
		<ul
			class="suggestions"
			role="listbox"
			bind:this={list}
			style:top={menuAt ? `${menuAt.top}px` : null}
			style:left={menuAt ? `${menuAt.left}px` : null}
		>
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
		/* Nu hvor alle der passer står på listen, skal den kunne rulle. Otte rækker
		   er nok til at det ses, at der er flere, uden at menuen dækker noten. */
		max-height: 15rem;
		overflow-y: auto;
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

		/* Linjelængden har en grænse, som skærmen ikke har.
		 *
		 * En note i fuld bredde på en stor skærm er linjer på hundrede og fyrre tegn,
		 * og øjet mister hvilken linje det var på, når det springer tilbage. Alle,
		 * der laver noget, man læser i, sætter det her — Apple Noter, Bear, Notion —
		 * og de sætter det omkring det samme sted.
		 *
		 * Kun teksten, ikke arket: værktøjslinjen og foden hører til ruden og skal
		 * blive i den. */
		/* Bredden vokser med ruden, men ikke i det uendelige.
		 *
		 * 46rem var for stramt: på en skærm, der er halvanden gang så bred, stod
		 * teksten i en smal søjle med en håndflade tom plads ved siden af — og
		 * kodeblokke og billeder, som er de to ting i en note, der *vil* have
		 * bredde, blev klemt sammen uden grund.
		 *
		 * clamp frem for et tal: en smal rude får det hele, en bred får en
		 * linjelængde, øjet kan finde tilbage i. Øvre grænse omkring 68rem, hvor en
		 * linje er lang nok til at rumme en kommando og kort nok til at læse. */
		/* Grænsen sættes som polstring og ikke som bredde, og det er rullebjælken,
		 * der afgør det.
		 *
		 * Det er *dette* element, der ruller. Var det også det, der blev smallere,
		 * fulgte rullebjælken med ind: den stod ved tekstsøjlens kant med en
		 * håndflade tom rude til højre for sig og svævede midt i arket. Med
		 * polstring er elementet lige så bredt som ruden — bjælken bliver ved
		 * kanten, hvor den hører til — og teksten er nøjagtig lige så bred som før.
		 *
		 * `max()` mod nul, fordi en smal rude ellers ville få negativ polstring, og
		 * `border-box` er sat globalt, så bredden er hele boksen og ikke boksen plus
		 * polstringen. Uden det ville det her give en vandret rullebjælke. */
		width: 100%;
		padding-right: max(0px, calc(100% - clamp(38rem, 92%, 68rem)));
	}

	.hidden {
		display: none;
	}

	/* Mens en fil holdes over noten. En ramme frem for en farvet flade: teksten
	   under skal stadig kunne læses, så man kan se hvor billedet lander. */
	.page.dropping {
		outline: 2px dashed var(--accent);
		outline-offset: 4px;
		border-radius: var(--radius-sm);
	}

	/* Kildeteksten. Monospace, fordi det er en fil — og fordi indrykningen i en
	   liste er indhold her og ikke pynt: den skal kunne tælles. */
	.source {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		line-height: 1.6;
		tab-size: 2;
		white-space: pre-wrap;
		border: 0;
		background: none;
		color: var(--ink);
		resize: none;
	}

	.src {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
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

	/* Renden skal kunne rumme det bredeste mærke.
	 *
	 * Mærket sættes uden for tekstkanten, inde i listens egen indrykning, og en fast
	 * indrykning på 16px rakte til et punkttegn og ikke til "10." — resten hang ud
	 * over kanten, hvor `.page`, som ruller lodret og derfor beskærer vandret,
	 * klippede det af. Man så et punktum uden sit tal.
	 *
	 * Samme rende til begge slags, frem for en bredere til den nummererede: mærket
	 * lægger sig op ad teksten uanset hvor bred renden er, så deler de den, flugter
	 * punkter og tal — og gør de ikke, står to lister under hinanden med hver sin
	 * venstrekant.
	 *
	 * Målt i em, så den følger skriftstørrelsen, og bred nok til tre cifre. */
	.page :global(ul),
	.page :global(ol) {
		margin: 0 0 var(--s2);
		padding-left: 2.2em;
	}

	.page :global(li) {
		margin-bottom: var(--s1);
	}

	/* Et billede fylder sin egen bredde og ikke mere. Et foto fra en telefon er
	   fire tusind pixels bredt, og uden det her stikker det ud af arket. */
	.page :global(img) {
		max-width: 100%;
		height: auto;
		border-radius: var(--radius-sm);
		display: block;
		margin: var(--s2) 0;
	}

	.page :global(blockquote) {
		margin: 0 0 var(--s2);
		padding-left: var(--s3);
		border-left: 2px solid var(--line-strong);
		color: var(--ink-muted);
	}

	/* Kopiér-knappen, øverst til højre i blokken.
	 *
	 * Synlig hele tiden, ikke kun ved hover. En knap, der først findes, når musen
	 * er det rigtige sted, er en knap, folk ikke ved er der — og på en berøringsskærm
	 * findes den slet ikke. Den er i stedet dæmpet nok til ikke at tage
	 * opmærksomhed fra koden.
	 *
	 * `z-index` sagt højt: uden det lå den bag blokkens eget indhold i
	 * stakrækkefølgen, og et klik ramte <pre> i stedet for knappen. */
	.page :global(pre) {
		position: relative;
	}

	.page :global(pre .copy) {
		position: absolute;
		z-index: 1;
		top: 6px;
		right: 6px;
		font-family: var(--font);
		font-size: var(--text-xs);
		padding: 2px var(--s2);
		border-radius: var(--radius-sm);
		background: rgb(255 255 255 / 0.08);
		color: rgb(255 255 255 / 0.55);
		transition: background var(--fast) var(--ease), color var(--fast) var(--ease);
	}

	.page :global(pre:hover .copy),
	.page :global(pre .copy:hover),
	.page :global(pre .copy:focus-visible) {
		background: rgb(255 255 255 / 0.2);
		color: #fff;
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
