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
	import { colorVar } from '$lib/colors.js';
	import NoteEditor from '$lib/components/NoteEditor.svelte';

	let notes = $state([]);

	/**
	 * Rækkefølgen, og hvorfor den kan vælges.
	 *
	 * Serveren svarer i én rækkefølge — senest rørt øverst — og det er den rigtige,
	 * når man arbejder: den note, man var i gang med, er den, man skal tilbage til.
	 * Den er også den eneste, der ikke kan svare på "hvad skrev jeg dengang".
	 * Tolv hundrede noter flyttet ind fra et andet program er et arkiv, og et arkiv
	 * læses efter, hvornår noget blev skrevet, og efter navn.
	 *
	 * Sorteret her frem for i en forespørgsel: hele listen er allerede hentet, den
	 * fylder ingenting, og et valg, der ikke koster et kald, kan skiftes så hurtigt
	 * som man kan ombestemme sig.
	 */
	const ORDERS = [
		{ key: 'updated', label: 'notes.sortUpdated' },
		{ key: 'created', label: 'notes.sortCreated' },
		{ key: 'title', label: 'notes.sortTitle' }
	];

	let order = $state('updated');

	$effect(() => {
		const saved = localStorage.getItem('notes.order');
		if (saved && ORDERS.some((o) => o.key === saved)) order = saved;
	});

	function setOrder(next) {
		order = next;
		localStorage.setItem('notes.order', next);
	}

	// Favoritter står øverst uanset rækkefølge. Det er hele meningen med at gøre en
	// note til favorit, og en sortering, der blandede dem ind igen, ville tage den.
	let ordered = $derived.by(() => {
		// En søgning har allerede en rækkefølge, og den er bedre end nogen af de
		// tre herunder: serveren har sorteret efter hvor godt hver note matchede.
		// Sorterede vi den om efter dato, ville vi kaste netop det væk, der gør en
		// søgning i tolv hundrede noter brugbar.
		if (query.trim()) return notes;
		return sortedByChoice();
	});

	const sortedByChoice = () =>
		[...notes].sort((a, b) => {
			if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
			if (order === 'title') {
				return (a.title || '').localeCompare(b.title || '', 'da', { sensitivity: 'base' });
			}
			const field = order === 'created' ? 'created_at' : 'updated_at';
			return new Date(b[field]).getTime() - new Date(a[field]).getTime();
		});

	// --- listen i grupper ----------------------------------------------------------
	//
	// Listen føltes tæt, og grunden var ikke afstanden mellem rækkerne: hver række
	// sagde tre ting, og to af dem gentog sig selv nedad. Datoen stod på hver eneste
	// række — også når tyve noter deler dag — og projektet fik en linje for sig selv.
	// Tre linjer pr. note, gange tolv hundrede.
	//
	// Gentagelserne flyttes derhen, hvor de siger noget. Datoen bliver til
	// overskriften over en gruppe, og så forsvinder den fra rækken; projektet bliver
	// en lille mærkat ude til højre. Noten går fra tre linjer til to, og luften
	// kommer af, at der er mindre at skrive — ikke af at listen bliver længere.

	/** Grupper, der er foldet sammen. Kun i denne session. */
	let folded = $state(new Set());

	function toggleGroup(key) {
		const next = new Set(folded);
		next.has(key) ? next.delete(key) : next.add(key);
		folded = next;
	}

	/**
	 * Hvilken dato en gruppe måles på: den, der er sorteret efter.
	 *
	 * Ellers ville listen stå i én rækkefølge og være grupperet efter en anden, og
	 * så er overskrifterne løgn — en note ville stå under "August" mellem to fra
	 * juli, fordi den var *rørt* i august og *oprettet* i juli.
	 */
	const groupDate = (note) => (order === 'created' ? note.created_at : note.updated_at);

	/**
	 * Hvad en gruppe hedder. I dag, i går, denne uge — og derefter måneden.
	 *
	 * Nøglen og etiketten er to ting: etiketten er sproget, og nøglen er den, der
	 * afgør om to noter hører sammen. Måneden som nøgle må bære året med sig, eller
	 * ville august 2026 og august 2025 blive én bunke.
	 */
	function bucketOf(note) {
		if (order === 'title') {
			// Sorteret på navn er der ingen dato at gruppere på, så det bliver
			// forbogstavet — og datoen kommer tilbage i rækken, hvor den så er den
			// eneste, der siger noget.
			const first = (note.title || '').trim().charAt(0).toUpperCase();
			const letter = /\p{L}|\p{N}/u.test(first) ? first : '#';
			return { key: 'a-' + letter, label: letter };
		}

		const at = new Date(groupDate(note));
		if (Number.isNaN(at.getTime())) return { key: 'ukendt', label: t('notes.groupUnknown') };

		const now = new Date();
		const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate());
		const days = Math.floor((midnight - new Date(at.getFullYear(), at.getMonth(), at.getDate())) / 86400000);

		if (days <= 0) return { key: 'i-dag', label: t('notes.groupToday') };
		if (days === 1) return { key: 'i-gaar', label: t('notes.groupYesterday') };
		if (days < 7) return { key: 'ugen', label: t('notes.groupWeek') };

		const label = at.toLocaleDateString(tag(), { month: 'long', year: 'numeric' });
		return { key: `m-${at.getFullYear()}-${at.getMonth()}`, label };
	}

	/**
	 * Listen, delt op.
	 *
	 * En søgning grupperes ikke: serveren har sorteret efter hvor godt hver note
	 * matchede, og at dele det op i måneder ville stille rækkefølgen om efter noget,
	 * der ikke er svaret på det, der blev spurgt om.
	 */
	let groups = $derived.by(() => {
		if (query.trim()) return [{ key: 'fundet', label: '', notes: ordered, plain: true }];

		const out = [];
		const favourites = ordered.filter((n) => n.pinned);
		if (favourites.length) {
			out.push({ key: 'favoritter', label: t('notes.groupFavourites'), notes: favourites, star: true });
		}

		let current = null;
		for (const note of ordered) {
			if (note.pinned) continue;
			const bucket = bucketOf(note);
			if (!current || current.key !== bucket.key) {
				current = { key: bucket.key, label: bucket.label, notes: [] };
				out.push(current);
			}
			current.notes.push(note);
		}
		return out;
	});

	/** Projektets egen farve, som i sidebjælken. */
	const projectColour = (id) => colorVar(app.projects.find((p) => p.id === id)?.color);
	let selected = $state(null);
	// Hvilken række der er valgt. Se open(): editoren venter på hele noten.
	let selectedId = $state(null);

	/**
	 * Flere ad gangen.
	 *
	 * Et sæt frem for et flag på noten, fordi markeringen hører til skærmen og ikke
	 * til noten: den forsvinder, når man går et andet sted hen, og den skal ikke
	 * gemmes nogen steder.
	 *
	 * `anchor` er den, et skift-klik måler fra. Uden den ville et interval skulle
	 * gættes ud fra "den valgte", og den flytter sig, hver gang man klikker.
	 */
	let picked = $state(new Set());
	let anchor = $state(null);
	let showArchive = $state(false);

	function rowClick(event, note) {
		const many = event.metaKey || event.ctrlKey;
		const range = event.shiftKey;

		if (!many && !range) {
			picked = new Set();
			anchor = note.id;
			open(note);
			return;
		}

		const next = new Set(picked);
		if (range && anchor) {
			// Fra ankeret til her, i den rækkefølge listen faktisk står i — ikke i
			// notens egen, som er en anden, når der er sorteret eller søgt.
			const ids = ordered.map((n) => n.id);
			const from = ids.indexOf(anchor);
			const to = ids.indexOf(note.id);
			if (from !== -1 && to !== -1) {
				for (const id of ids.slice(Math.min(from, to), Math.max(from, to) + 1)) next.add(id);
			}
		} else {
			// Den, der lige er åbnet, hører med i markeringen: ellers ville et
			// ⌘-klik nummer to markere to noter og glemme den, man stod på.
			if (!next.size && selectedId) next.add(selectedId);
			next.has(note.id) ? next.delete(note.id) : next.add(note.id);
			anchor = note.id;
		}
		picked = next;
	}

	/** Arkivér, tag frem igen, eller slet — for det, der er markeret. */
	async function bulk(change) {
		const ids = [...picked];
		if (!ids.length) return;
		if (change.delete && !confirm(t('notes.deleteMany', { count: ids.length }))) return;
		try {
			await api.noteBulk({ ids, ...change });
			picked = new Set();
			if (ids.includes(selectedId)) open(null);
			await load(query);
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	/** Én note lagt væk, uden at markere den først. */
	async function archiveOne(note) {
		try {
			await api.noteBulk({ ids: [note.id], archived: !note.archived_at });
			if (selectedId === note.id) open(null);
			await load(query);
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}
	let query = $state('');

	/**
	 * Det, der står i feltet, og det, der er søgt efter.
	 *
	 * To ting, fordi de ikke er den samme: `draftQuery` opdateres ved hvert
	 * tastetryk,
	 * så feltet føles som et felt, og `query` halter en kvart sekund bagefter, fordi
	 * det er den, der koster en forespørgsel. Uden det sendte "kaffe" fem søgninger
	 * gennem tolv hundrede noter, hvoraf de fire var forældede, inden de kom
	 * tilbage — og svarene kunne lande i den forkerte rækkefølge.
	 */
	let draftQuery = $state('');
	let typing = $state(false);
	let searchTimer;

	function search(next) {
		draftQuery = next;
		typing = next.trim() !== '' && next !== query;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			query = draftQuery;
			typing = false;
		}, 250);
	}
	let status = $state('loading');
	let saving = $state(false);

	// The text being edited, kept apart from the note it came from so a save that
	// arrives late cannot overwrite what has been typed since.
	let draft = $state('');
	let timer;

	$effect(() => {
		showArchive;
		load(query);
	});

	// Arriving from somewhere that names a note — a task's panel, a project's page —
	// opens it rather than dropping the person on a list to find it again.
	let asked = $derived($page.url.searchParams.get('note'));
	$effect(() => {
		if (!asked || selectedId === asked) return;
		const found = notes.find((n) => n.id === asked);
		if (found) open(found);
		else api.note(asked).then(open).catch(() => {});
	});

	async function load(q) {
		try {
			const params = q ? { q } : showArchive ? { archived: '1' } : {};
			notes = (await api.notes(params)).notes ?? [];
			status = 'ready';
			if (selectedId && !notes.some((n) => n.id === selectedId)) open(notes[0] ?? null);
		} catch (e) {
			status = 'failed';
			app.toast(humanMessage(e));
		}
	}

	/**
	 * Åbner en note — og henter den hel først, hvis listen kun har uddraget.
	 *
	 * Listen bærer `preview` og en tom `body`, så en liste på tolv hundrede noter
	 * ikke er tolv hundrede hele noter. Editoren må aldrig få det uddrag: den ville
	 * gemme det efter sin pause, og noten ville være skåret ned til sit eget uddrag
	 * af ingenting andet end at være blevet kigget på.
	 *
	 * Derfor hentes den hele her. Rækken vises med det samme, så det ikke føles som
	 * at vente; teksten kommer, når den er der.
	 */
	async function open(note) {
		clearTimeout(timer);
		if (!note) {
			selectedId = null;
			selected = null;
			draft = '';
			return;
		}

		// Rækken fremhæves med det samme; editoren får først noten, når den er hel.
		//
		// De to er skilt ad, fordi editoren indlæser sin tekst, når notens *id*
		// skifter — og hvis den fik uddraget først og hele noten bagefter, ville
		// id'et være det samme begge gange, så den anden aldrig blev vist. Man ville
		// sidde med en tom note, der lige havde haft en titel.
		selectedId = note.id;
		if (note.body) {
			selected = note;
			draft = note.body;
			return;
		}
		selected = null;

		try {
			const full = await api.note(note.id);
			// Kun hvis den stadig er den, der er valgt: skifter man note, mens
			// hentningen er undervejs, må den forrige ikke lande oven i den nye.
			if (selectedId !== note.id) return;
			selected = full;
			draft = full.body ?? '';
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	/**
	 * The first line or so of a note, without its marks.
	 *
	 * The list showed the Markdown as stored — "# Møde med Anders" and "**fed**" —
	 * which is exactly the thing the editor was built to stop showing. What is
	 * wanted here is the sentence, not the notation.
	 */
	/**
	 * Uddraget, uden titlen foran.
	 *
	 * Titlen *er* første linje — det er hele reglen for, hvad en note hedder — så et
	 * uddrag, der begynder fra toppen, siger navnet én gang til. Det var svært at se,
	 * så længe en dato stod imellem dem; nu står de oven på hinanden, og så står der
	 * "Verdande" og under det "Verdande Forbindelse til connectoren".
	 */
	function preview(note) {
		const body = note.preview ?? note.body ?? '';
		const nl = body.indexOf('\n');
		const rest = nl === -1 ? '' : body.slice(nl + 1);
		// Kun hvis den første linje faktisk *er* titlen. Et uddrag fra serveren er
		// afkortet, så en lang første linje kan være hele uddraget — og så er der
		// ikke andet at vise end den.
		const withoutTitle = plain(rest);
		return withoutTitle || plain(body);
	}

	function plain(body) {
		return (body ?? '')
			// Et billede har ingen ord i sig. Uden det her stod der
			// "![](/api/v1/attachments/01a0…" i uddraget af hver note med et foto i,
			// hvilket er den eneste tekst i listen, ingen har skrevet.
			.replace(/!\[[^\]]*\]\([^)]*\)/g, '')
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

	/** Hvilken dato rækken viser: den, der sorteres efter. */
	const shownDate = (note) => (order === 'created' ? note.created_at : note.updated_at);

	/** Begge datoer, til den, der holder musen stille. */
	const stamp = (note) =>
		`${t('notes.sortCreated')}: ${new Date(note.created_at).toLocaleString(tag())}\n` +
		`${t('notes.sortUpdated')}: ${new Date(note.updated_at).toLocaleString(tag())}`;

	function projectName(id) {
		return app.projects.find((p) => p.id === id)?.name ?? '';
	}

	// Trukket ind på et projekt i sidebjælken. Nyttelasten er notens id; sidebjælken
	// kender allerede formen fra opgaver.
	let dragging = $state(null);

	function onDragStart(event, note) {
		dragging = note.id;
		// Trækker man en note, der er markeret, følger hele markeringen med. Trækker
		// man en, der ikke er, er det den ene — ellers ville et træk i en tilfældig
		// række flytte halvtreds noter, man havde glemt var markeret.
		const ids = picked.has(note.id) ? [...picked] : [note.id];
		startDrag(event, NOTE, ids.join(' '));
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
			// Svaret er hele noten. Rækken viser `preview`, så den skal med — ellers
			// står linjen tom, indtil siden hentes igen.
			const forList = { ...saved, preview: saved.body ?? '' };
			notes = notes.map((n) => (n.id === id ? forList : n));
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
			<div class="tools">
				<!-- En select frem for tre knapper: rækkefølgen vælges sjældent, og tre
				     knapper ville tage plads fra listen hver eneste dag for at spare et
				     klik en gang imellem. -->
				<select
					class="order"
					value={order}
					onchange={(e) => setOrder(e.currentTarget.value)}
					aria-label={t('notes.sortBy')}
				>
					{#each ORDERS as o}
						<option value={o.key}>{t(o.label)}</option>
					{/each}
				</select>
				<button
					class="archive-toggle"
					class:on={showArchive}
					onclick={() => { showArchive = !showArchive; picked = new Set(); }}
					aria-pressed={showArchive}
					aria-label={t('notes.showArchive')}
					title={t('notes.showArchive')}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M3 7h18v3H3zM5 10v9h14v-9M10 14h4" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" />
					</svg>
				</button>
				<button class="new" onclick={create} aria-label={t('notes.new')}>+</button>
			</div>
		</div>

		<!-- Samme form som søgefeltet i toppen: samme flade, samme kant, samme
		     radius. Det var en pille med et forstørrelsesglas tegnet ind som et
		     baggrundsbillede med farven skrevet i hånden — så det var det ene sted i
		     programmet, der ikke fulgte temaet, og det kunne ses. -->
		<div class="searchbox" class:busy={typing}>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<circle cx="11" cy="11" r="7" fill="none" stroke="currentColor" stroke-width="2" />
				<path d="M20 20l-3.5-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
			</svg>
			<input
				type="search"
				value={draftQuery}
				oninput={(e) => search(e.currentTarget.value)}
				placeholder={t('notes.search')}
				aria-label={t('notes.search')}
			/>
			{#if draftQuery}
				<button class="clear" onclick={() => search('')} aria-label={t('notes.clearSearch')}>×</button>
			{/if}
		</div>
		{#if picked.size}
			<!-- Kun mens der er markeret noget. En bjælke, der altid står der, er en
			     bjælke, der tager plads fra listen hver dag for at gøre gavn sjældent. -->
			<div class="picked-bar">
				<span>{t('notes.picked', { count: picked.size })}</span>
				<button onclick={() => bulk({ archived: !showArchive })}>
					{showArchive ? t('notes.unarchive') : t('notes.archive')}
				</button>
				<button class="danger" onclick={() => bulk({ delete: true })}>{t('notes.delete')}</button>
				<button onclick={() => (picked = new Set())}>{t('notes.clearPick')}</button>
			</div>
		{:else if query && status === 'ready'}
			<p class="found">{t('notes.found', { count: notes.length })}</p>
		{/if}

		{#if status === 'loading'}
			<p class="hint">{t('history.loading')}</p>
		{:else if !notes.length}
			<p class="hint">
				{query ? t('notes.noneFound') : showArchive ? t('notes.emptyArchive') : t('notes.noneYet')}
			</p>
		{:else}
			{#each groups as group (group.key)}
				<!-- Overskriften klæber, mens man ruller. Med tolv hundrede noter er det
				     forskellen på at rulle og på at lede: man kan altid se, hvilken måned
				     man er nede i. -->
				{#if !group.plain}
					<h3 class="group">
						<!-- Ingen `aria-label`. Den ville erstatte knappens indhold som dens
						     navn, og knappen *er* overskriften — så overskriften ville hedde
						     "Fold I dag sammen" i stedet for "I dag". `aria-expanded` siger
						     tilstanden, som den gør på gruppehovederne i sidebjælken. -->
						<button onclick={() => toggleGroup(group.key)} aria-expanded={!folded.has(group.key)}>
							{#if group.star}
								<span class="mark favmark" aria-hidden="true">★</span>
							{:else}
								<span class="mark" class:closed={folded.has(group.key)} aria-hidden="true">▾</span>
							{/if}
							{group.label}
							<span class="count">{group.notes.length}</span>
						</button>
					</h3>
				{/if}
				{#if !folded.has(group.key)}
					<ul>
						{#each group.notes as note (note.id)}
						<li>
							<div class="rowline">
								<button
									class="row"
									class:on={selectedId === note.id}
									class:picked={picked.has(note.id)}
									onclick={(e) => rowClick(e, note)}
									draggable="true"
									ondragstart={(e) => onDragStart(e, note)}
									ondragend={() => (dragging = null)}
								>
									<!-- Titel og projekt på linje ét, uddrag på linje to. Datoen står
									     i gruppens overskrift og ikke her — undtagen når der er sorteret
									     på navn, for så er der ingen overskrift, der siger den, og så er
									     den det eneste tilbage, der gør. -->
									<span class="head">
										<strong>{note.title || t('notes.untitled')}</strong>
										{#if order === 'title'}
											<span class="when" title={stamp(note)}>{when(shownDate(note))}</span>
										{/if}
										{#if note.project_id}
											<!-- Projektets egen farve, den samme som prikken i sidebjælken.
											     Mærkaten sagde det samme som alle de andre i en grå, der var
											     lysere end uddraget — og projektet er dét, man skanner efter,
											     når man leder i tolv hundrede noter. Nu kan Hjem kendes fra
											     Claude uden at læse ordet. -->
											<span class="filed" style="--project: {projectColour(note.project_id)}">
												<span class="dot" aria-hidden="true"></span>
												{projectName(note.project_id)}
											</span>
										{/if}
									</span>
									<span class="preview">{preview(note)}</span>
								</button>
								<!-- De to knapper står oven på hinanden i en smal søjle.
								     Ved siden af hinanden tog de toogfyrre pixels af en liste,
								     der er tre hundrede bred — og de bruges sjældent, mens
								     titlen og uddraget læses hele tiden. -->
								<div class="rowactions">
								<!-- Én note lagt væk, uden at markere den først. Markeringen er
								     til flere; det her er til den ene, man står med — og i
								     arkivet er det vejen tilbage. -->
								<button
									class="archive-one"
									onclick={() => archiveOne(note)}
									aria-label={note.archived_at ? t('notes.unarchive') : t('notes.archiveOne')}
									title={note.archived_at ? t('notes.unarchive') : t('notes.archiveOne')}
								>
									<svg viewBox="0 0 24 24" aria-hidden="true">
										<path d="M3 7h18v3H3zM5 10v9h14v-9M10 14h4" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" />
									</svg>
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
							</div>
						</li>
						{/each}
					</ul>
				{/if}
			{/each}
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
		gap: var(--s2);
	}

	.tools {
		display: flex;
		align-items: center;
		gap: var(--s1);
		min-width: 0;
	}

	.order {
		font-size: var(--text-xs);
		color: var(--ink-muted);
		border: 0;
		background: none;
		padding: 2px var(--s1);
		max-width: 13ch;
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
	/* Formet som søgefeltet i toppen — samme flade, kant og radius — så de to
	   søgefelter på skærmen ligner hinanden i stedet for at være to beslutninger.
	   Ikonet er en rigtig SVG med `currentColor`, ikke et baggrundsbillede med en
	   farve skrevet ind: den gamle var #8b918d uanset tema. */
	.searchbox {
		display: flex;
		align-items: center;
		gap: var(--s2);
		padding: var(--s2) var(--s3);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		color: var(--ink-faint);
		transition: border-color var(--fast) var(--ease);
	}

	.searchbox:focus-within {
		border-color: var(--line-strong);
	}

	/* Mens det tastede endnu ikke er søgt efter. Et hint om at der kommer noget,
	   frem for en liste, der ser forkert ud i en kvart sekund. */
	.searchbox.busy {
		border-color: var(--accent);
	}

	.searchbox svg {
		width: 15px;
		height: 15px;
		flex: none;
	}

	.searchbox input {
		flex: 1;
		min-width: 0;
		appearance: none;
		-webkit-appearance: none;
		border: 0;
		background: none;
		color: var(--ink);
		font-size: var(--text-sm);
		outline: none;
	}

	.clear {
		flex: none;
		width: 18px;
		height: 18px;
		border-radius: var(--radius-full);
		color: var(--ink-faint);
		font-size: 15px;
		line-height: 1;
	}

	.clear:hover {
		background: var(--surface-sunken);
		color: var(--ink);
	}

	/* Bjælken over listen, mens der er markeret noget. Den erstatter antallet af
	   fundne frem for at lægge sig oven i det: kun ét af de to er relevant ad
	   gangen. */
	.picked-bar {
		display: flex;
		align-items: center;
		gap: var(--s2);
		flex-wrap: wrap;
		padding: var(--s2) var(--s1);
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.picked-bar button {
		font-size: var(--text-xs);
		color: var(--ink);
		padding: 2px var(--s2);
		border-radius: var(--radius-sm);
		border: 1px solid var(--line);
	}

	.picked-bar button:hover {
		background: var(--surface);
	}

	.picked-bar button.danger {
		color: var(--danger);
		border-color: var(--danger-sunken);
	}

	/* En markeret række.
	 *
	 * En kant og en tone, ikke en tone alene: `--accent-sunken` er meget lys i lyst
	 * tema, og tre markerede rækker var næsten ikke til at skelne fra tre, der ikke
	 * var — hvilket er en dårlig ting at være i tvivl om, lige inden man trykker
	 * Slet. Kanten kan ses uanset tema og uanset hvor lys tonen er. */
	.row.picked {
		background: var(--accent-sunken);
		box-shadow: inset 3px 0 0 var(--accent);
	}

	/* Vises som stjernen: usynlig, indtil rækken er under musen, så listen er
	   rolig at skanne. */
	.archive-one {
		flex: none;
		width: 22px;
		height: 22px;
		color: var(--ink-faint);
		border-radius: var(--radius-sm);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	.archive-one svg {
		width: 14px;
		height: 14px;
	}

	.rowline:hover .archive-one,
	.archive-one:focus-visible {
		opacity: 1;
	}

	.archive-one:hover {
		color: var(--ink);
		background: var(--surface);
	}

	.archive-toggle {
		width: 24px;
		height: 24px;
		color: var(--ink-faint);
		border-radius: var(--radius-sm);
	}

	.archive-toggle svg {
		width: 15px;
		height: 15px;
	}

	.archive-toggle:hover {
		color: var(--ink);
		background: var(--surface);
	}

	.archive-toggle.on {
		color: var(--accent);
		background: var(--accent-sunken);
	}

	.found {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding: 0 var(--s1);
	}

	/* Safaris egen ryd-knap ville sidde oven i vores egen. */
	.searchbox input::-webkit-search-decoration,
	.searchbox input::-webkit-search-cancel-button {
		-webkit-appearance: none;
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		overflow-y: auto;
		min-height: 0;
	}

	/* Rækker uden for skærmen koster ingenting.
	 *
	 * Listen tegner alle noterne. Med ti var det ligegyldigt; med tolv hundrede tog
	 * første tegning to sekunder, og browseren gik i knæ — for hver eneste række
	 * blev der lavet layout og malet, også de tusind, ingen kan se.
	 *
	 * `content-visibility: auto` lader browseren springe netop det over for det,
	 * der er uden for billedet, og tage det, når man ruller derhen. Ingen JavaScript,
	 * ingen rullematematik, ingen liste, der kan komme ud af trit med sig selv —
	 * hvilket er hele grunden til at prøve det her før en vinduesvisning.
	 *
	 * `contain-intrinsic-size` er gættet på, hvor høj en uset række er. Uden det
	 * ville rullebjælken tro, at listen er tom, og hoppe, mens man ruller. Tallet er
	 * målt på en rigtig række: titel plus én linje uddrag. */
	li {
		content-visibility: auto;
		contain-intrinsic-size: auto 54px;
	}

	.rowline {
		display: flex;
		align-items: center;
		gap: var(--s1);
	}

	/* De sjældne handlinger i en søjle, ikke en række. To knapper ved siden af
	   hinanden er toogfyrre pixels taget fra en liste på tre hundrede — og de er
	   det, man rører en gang imellem, mens titlen er det, man læser hele tiden. */
	.rowactions {
		display: flex;
		flex-direction: column;
		gap: 1px;
		flex: none;
	}

	.star {
		flex: none;
		width: 22px;
		height: 22px;
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

	/* --- grupperne ----------------------------------------------------------
	 *
	 * Overskriften klæber. Med tolv hundrede noter er det forskellen på at rulle og
	 * på at lede: man kan altid se, hvilken måned man er nede i.
	 *
	 * Baggrunden er ikke gennemsigtig. En klæbende overskrift uden en er en
	 * overskrift med rækkerne kørende igennem sig. */
	h3.group {
		position: sticky;
		top: 0;
		z-index: 1;
		margin: 0;
		background: var(--ground);
		border-bottom: 1px solid var(--line);
	}

	h3.group button {
		display: flex;
		align-items: center;
		gap: var(--s2);
		width: 100%;
		padding: var(--s2) var(--s2);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.07em;
		text-transform: uppercase;
		color: var(--ink-faint);
	}

	h3.group button:hover {
		color: var(--ink-muted);
	}

	h3.group .mark {
		flex: none;
		width: 0.75em;
		font-size: 0.85em;
		line-height: 1;
		color: var(--ink-faint);
		transition: transform var(--fast) var(--ease);
	}

	h3.group .mark.closed {
		transform: rotate(-90deg);
	}

	/* Sit eget navn og ikke `.star`. Rækkens favoritknap hedder også det og er
	   gennemsigtig, indtil man peger på rækken — så overskriftens stjerne arvede
	   den gennemsigtighed og stod der aldrig. To ting, der ligner hinanden, er
	   ikke det samme, og et delt klassenavn er en påstand om, at de er. */
	h3.group .favmark {
		color: var(--accent);
		font-size: 1em;
	}

	h3.group .count {
		margin-left: auto;
		font-weight: 500;
		letter-spacing: 0;
	}

	/* Stregen mellem rækkerne er væk.
	 *
	 * Grupperne giver strukturen nu, og en streg pr. note er tolv hundrede streger.
	 * Luften kommer af, at rækken siger to ting i stedet for tre — ikke af at listen
	 * bliver længere. */
	.row {
		display: block;
		flex: 1;
		min-width: 0;
		text-align: left;
		/* Plads til højre for de to knapper, der ligger oven på rækken. Uden den
		   løber uddraget ind under stjernen — usynligt, indtil man peger på rækken
		   og knappen dukker op oven i ordene. */
		padding: var(--s2) 26px calc(var(--s2) + 2px) var(--s2);
		border-radius: var(--radius);
		color: var(--ink-muted);
	}

	/* Titel, dato og projekt på én linje. Projektet ude til højre, hvor øjet kan
	   løbe ned ad søjlen uden at læse resten. */
	.head {
		display: flex;
		align-items: baseline;
		gap: var(--s2);
		min-width: 0;
	}

	.head strong {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
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

	.when {
		flex: none;
		color: var(--ink-muted);
	}

	/* `display: block`, og det er ikke pynt.
	 *
	 * Uddraget lå før inde i en flex-boks, der ejede afkortningen. Da datoen flyttede
	 * op i gruppens overskrift, forsvandt den boks — og et inline-element klippes
	 * ikke af `overflow: hidden`, så den sidste sætning løb videre ind under
	 * favoritstjernen. Reglerne skal stå på det element, der faktisk skal klippes. */
	.preview {
		display: block;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Projektets egen farve, den samme som prikken i sidebjælken.
	 *
	 * Mærkaten sagde det samme som alle de andre i en grå, der var lysere end
	 * uddraget — og projektet er dét, man skanner efter, når man leder i tolv
	 * hundrede noter. En prik i projektets farve og en tekst, der ikke er lysere end
	 * det, den står ved siden af: Hjem kan kendes fra Claude uden at ordet læses.
	 *
	 * Prikken bærer farven, ikke teksten. Fem projekters farver som skriftfarve på
	 * en lys flade er fem forskellige grader af læsbarhed, og den lyseste af dem er
	 * ikke læsbar. */
	.filed {
		display: flex;
		align-items: center;
		gap: 0.375em;
		flex: none;
		margin-left: auto;
		max-width: 45%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.filed .dot {
		width: 6px;
		height: 6px;
		flex: none;
		border-radius: var(--radius-full);
		background: var(--project, var(--line-strong));
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
		/* Luft nok til at teksten ikke rører kanten.
		 *
		 * Vandret vokser den med ruden: på en bred skærm skal et ark have marginer,
		 * ikke bare en kant — clamp holder den mellem en rimelig mindste og en, der
		 * ikke bliver til et vindue med tekst i midten. */
		padding: var(--s4) clamp(var(--s4), 3.5%, var(--s6));
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
