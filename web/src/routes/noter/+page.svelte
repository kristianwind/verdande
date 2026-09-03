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
	import { goto } from '$app/navigation';
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

		// Stort forbogstav på måneden: Intl giver "august 2026" på dansk, og en
		// overskrift begynder med stort.
		const month = at.toLocaleDateString(tag(), { month: 'long', year: 'numeric' });
		const label = month.charAt(0).toUpperCase() + month.slice(1);
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

		// Andres noter først, samlet. Som Favoritter-gruppen vises den kun, når der
		// er noget i den — har man ingen delte noter, findes overskriften ikke og
		// tager ingen plads. En delt note bor kun her, ikke også nede i en tidsbunke.
		const sharedWithMe = ordered.filter((n) => n.shared_with_me);
		if (sharedWithMe.length) {
			out.push({
				key: 'delt-med-mig',
				label: t('notes.groupSharedWithMe'),
				notes: sharedWithMe,
				people: true
			});
		}

		const favourites = ordered.filter((n) => n.pinned && !n.shared_with_me);
		if (favourites.length) {
			out.push({ key: 'favoritter', label: t('notes.groupFavourites'), notes: favourites, star: true });
		}

		let current = null;
		for (const note of ordered) {
			if (note.pinned || note.shared_with_me) continue;
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

	// Deling af den åbne note med enkeltpersoner. Kun ejeren ser panelet, så det
	// hentes kun for en note, man selv har lavet.
	let shares = $state([]);
	let shareCandidates = $state([]);
	let sharePick = $state('');
	let shareRole = $state('viewer');
	let shareBusy = $state(false);
	const ownsSelected = $derived(!!selected && selected.created_by === app.user?.id);

	// The whole of sharing — a project and named people — lives in a popover behind
	// one button, so the note's footer stays a status and three buttons rather than
	// a row that runs off its own width.
	let shareOpen = $state(false);
	let shareWrap = $state(null);
	// Closing on a switch of note, and only then: the panel belongs to the note it
	// was opened on. Tracked by id rather than by the object, so a save that returns
	// a fresh copy of the same note does not slam the panel shut mid-use.
	let sharePanelFor = $state(null);
	$effect(() => {
		const id = selected?.id ?? null;
		if (id !== sharePanelFor) {
			shareOpen = false;
			sharePanelFor = id;
		}
	});
	function onShareOutside(event) {
		if (shareOpen && shareWrap && !shareWrap.contains(event.target)) shareOpen = false;
	}

	$effect(() => {
		const id = selected?.id;
		// Nulstil ved hvert skift, så den forrige notes folk ikke står et øjeblik
		// under den nye.
		shares = [];
		shareCandidates = [];
		sharePick = '';
		if (!id || selected.created_by !== app.user?.id) return;
		let alive = true;
		api
			.noteShares(id)
			.then((r) => {
				if (!alive || selected?.id !== id) return;
				shares = r.shares ?? [];
				shareCandidates = r.candidates ?? [];
			})
			.catch(() => {});
		return () => {
			alive = false;
		};
	});

	const byName = (a, b) => (a.name ?? '').localeCompare(b.name ?? '');

	async function addShare() {
		if (!selected || !sharePick || shareBusy) return;
		const person = shareCandidates.find((p) => p.id === sharePick);
		shareBusy = true;
		try {
			await api.shareNote(selected.id, sharePick, shareRole);
			shares = [...shares, { user: person, role: shareRole }].sort((a, b) => byName(a.user, b.user));
			shareCandidates = shareCandidates.filter((p) => p.id !== sharePick);
			app.toast(t('notes.sharedToast', { name: person?.name ?? '' }));
			sharePick = '';
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			shareBusy = false;
		}
	}

	async function changeShareRole(userId, role) {
		try {
			await api.shareNote(selected.id, userId, role);
			shares = shares.map((s) => (s.user.id === userId ? { ...s, role } : s));
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function removeShare(userId) {
		const person = shares.find((s) => s.user.id === userId)?.user;
		try {
			await api.unshareNote(selected.id, userId);
			shares = shares.filter((s) => s.user.id !== userId);
			if (person) shareCandidates = [...shareCandidates, person].sort(byName);
			app.toast(t('notes.unsharedPerson', { name: person?.name ?? '' }));
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

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

	/**
	 * Follows a link from the panel to the thing it names.
	 *
	 * A [[note]] resolves by its title to a note — the one on the list if it is
	 * there, otherwise looked up — and opens it in place. A #project and a task go
	 * to their own pages. Until now these were text and nothing else: the note said
	 * it pointed somewhere and there was no way to go.
	 */
	async function openLink(link) {
		if (link.kind === 'note') {
			const wanted = link.target_id.toLowerCase();
			const here = notes.find((n) => (n.title ?? '').toLowerCase() === wanted);
			if (here) {
				open(here);
				return;
			}
			try {
				const found = (await api.notes({ q: link.target_id })).notes ?? [];
				const hit = found.find((n) => (n.title ?? '').toLowerCase() === wanted) ?? found[0];
				if (hit) open(hit);
				else app.toast(t('notes.linkMissing'));
			} catch (e) {
				app.toast(humanMessage(e));
			}
			return;
		}
		if (link.kind === 'project') {
			const project = app.projects.find(
				(p) => p.name.toLowerCase() === link.target_id.toLowerCase()
			);
			if (project) goto(`/projekt/${project.id}`);
			return;
		}
		if (link.kind === 'task') {
			goto(`/opgave/${link.target_id}`);
		}
	}


</script>

<svelte:head><title>{t('notes.title')} · verdande</title></svelte:head>

<svelte:window
	onclick={onShareOutside}
	onkeydown={(e) => {
		if (e.key === 'Escape' && shareOpen) shareOpen = false;
	}}
/>

<!-- `has-note` drives the phone layout: one column, showing the list until a note
     is opened and the editor once one is. On a wide screen both are always up and
     the class does nothing. -->
<div class="notes" class:has-note={selected}>
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
			<!-- Én rulleboks om alle grupperne.
			     Før var der ét `<ul>`, og *det* rullede. Da listen blev delt i grupper,
			     blev hver gruppe sit eget `<ul>` — og dermed sit eget flex-element med
			     `overflow-y: auto`. Flex-elementer skrumper som udgangspunkt, og et
			     element, der skrumper og klipper, viser ingenting: alle overskrifter
			     stod med deres tal, og der var ikke en eneste note under nogen af dem.
			     Rulningen hører til kassen om dem alle, ikke til hver af dem. -->
			<div class="list">
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
								{:else if group.people}
									<span class="mark peoplemark" aria-hidden="true">
										<svg viewBox="0 0 24 24" aria-hidden="true">
											<circle cx="9" cy="8" r="3.2" fill="none" stroke="currentColor" stroke-width="2" />
											<path d="M3.5 19c0-3 2.5-5 5.5-5s5.5 2 5.5 5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
											<path d="M16 6.2a3 3 0 0 1 0 5.6M17.5 19c0-2.2-1-3.9-2.6-4.7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
										</svg>
									</span>
								{:else}
									<!-- No arrow, to match the sidebar: the box folds on a click,
									     the star and the people icon are the marks that mean
									     something, and a date group has none — an empty spacer, so
									     every label still starts at the same place. -->
									<span class="mark" aria-hidden="true"></span>
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
										<!-- Tre linjer, som Apple Noter: titlen, så datoen og uddraget, og
										     nederst hvor noten er lagt. Datoen står forrest på linje to og
										     venstrestillet, ikke ude til højre. -->
										<span class="head">
											<strong>{note.title || t('notes.untitled')}</strong>
										</span>
										<span class="preview">
											<span class="when" title={stamp(note)}>{when(shownDate(note))}</span>
											<span class="snippet">{preview(note)}</span>
										</span>
										<!-- Linje tre: hvor noten er lagt, med den samme mappe som grupperne i
										     menuen — projektets navn, ejerens hvis den er en andens, ellers bare
										     "Noter", når den ikke hører til et projekt. -->
										<span class="filed">
											{#if note.shared_with_me && note.owner}
												<svg class="filed-icon" viewBox="0 0 16 16" aria-hidden="true">
													<circle cx="8" cy="6" r="2.3" fill="none" stroke="currentColor" stroke-width="1.4" />
													<path d="M3.6 13c0-2.5 2-4 4.4-4s4.4 1.5 4.4 4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
												</svg>
												<span class="filed-name" title={t('notes.sharedBy', { name: note.owner.name })}>{note.owner.name}</span>
											{:else if note.project_id}
												<svg class="filed-icon" viewBox="0 0 16 16" aria-hidden="true">
													<path d="M2 4.6a1 1 0 011-1h3.1l1.4 1.5H13a1 1 0 011 1v6.3a1 1 0 01-1 1H3a1 1 0 01-1-1z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round" />
												</svg>
												<span class="filed-name">{projectName(note.project_id)}</span>
											{:else}
												<svg class="filed-icon" viewBox="0 0 16 16" aria-hidden="true">
													<path d="M2 4.6a1 1 0 011-1h3.1l1.4 1.5H13a1 1 0 011 1v6.3a1 1 0 01-1 1H3a1 1 0 01-1-1z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round" />
												</svg>
												<span class="filed-name">{t('notes.title')}</span>
											{/if}
										</span>
									</button>
									<!-- De to knapper står oven på hinanden i en smal søjle.
									     Ved siden af hinanden tog de toogfyrre pixels af en liste,
									     der er tre hundrede bred — og de bruges sjældent, mens
									     titlen og uddraget læses hele tiden. -->
									<!-- Ingen knapper på en note, en anden ejer: at arkivere eller
									     stjernemarkere ændrer selve noten, og den er ikke min at lægge
									     væk eller pinne. Rækken læses og åbnes; resten er ejerens. -->
									{#if !note.shared_with_me}
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
									{/if}
								</div>
							</li>
							{/each}
					</ul>
				{/if}
				{/each}
			</div>
		{/if}
	</aside>

	<section class="editor">
		{#if selected}
			<!-- Phone only (hidden on a wide screen by CSS): the way back to the list,
			     since there the editor fills the screen and the list is not beside it. -->
			<button class="back" onclick={() => open(null)}>‹ {t('notes.title')}</button>
			<NoteEditor
				note={selected}
				notes={notes}
				onopennote={(title) => openLink({ kind: 'note', target_id: title })}
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
							<button class="link" onclick={() => openLink(link)} title={t('notes.linkOpen')}>
								{label(link)}
							</button>
						{/each}
					</span>
				{/if}

				<div class="actions">
					<!-- All of sharing behind one button. A note has two ways in — a
					     project and named people — and both belong to the owner; the
					     panel gathers them rather than lining them up across the footer. -->
					{#if ownsSelected}
						<div class="sharewrap" bind:this={shareWrap}>
							<button
								class="button"
								class:on={shareOpen}
								aria-expanded={shareOpen}
								aria-haspopup="dialog"
								onclick={() => (shareOpen = !shareOpen)}
							>
								{t('notes.shareButton')}{#if shares.length}<span class="count">{shares.length}</span>{/if}
							</button>

							{#if shareOpen}
								<div class="sharepanel" role="dialog" aria-label={t('notes.shareButton')}>
									<!-- Filing it in a project: everybody who can read the project
									     reads the note. A real <label> around the select, so it is
									     named for a screen reader and not only for the eye. -->
									<section>
										<label class="field">
											<span class="phead">{t('notes.shareWith')}</span>
											<select
												aria-label={t('notes.shareWith')}
												value={selected.project_id ?? ''}
												onchange={(e) => share(e.currentTarget.value)}
											>
												<option value="">{t('notes.private')}</option>
												{#each app.projects.filter((p) => !p.is_inbox) as project (project.id)}
													<option value={project.id}>{project.name}</option>
												{/each}
											</select>
										</label>
									</section>

									<!-- Handing it to named people, without a project between. -->
									<section>
										<span class="phead">{t('notes.sharePeople')}</span>
										{#if shares.length}
											<ul class="sharelist">
												{#each shares as sh (sh.user.id)}
													<li>
														<span class="who">
															<span
																class="avatar"
																style="--who: {sh.user.avatar_color}"
																aria-hidden="true"
															></span>
															{sh.user.name}
														</span>
														<select
															value={sh.role}
															onchange={(e) => changeShareRole(sh.user.id, e.currentTarget.value)}
														>
															<option value="viewer">{t('notes.shareRoleViewer')}</option>
															<option value="editor">{t('notes.shareRoleEditor')}</option>
														</select>
														<button
															class="unshare"
															onclick={() => removeShare(sh.user.id)}
															title={t('notes.shareRemove')}
															aria-label={t('notes.shareRemove')}>×</button
														>
													</li>
												{/each}
											</ul>
										{:else}
											<span class="none">{t('notes.shareNobody')}</span>
										{/if}
										{#if shareCandidates.length}
											<div class="addshare">
												<select bind:value={sharePick}>
													<option value="">{t('notes.sharePick')}</option>
													{#each shareCandidates as person (person.id)}
														<option value={person.id}>{person.name}</option>
													{/each}
												</select>
												<select bind:value={shareRole}>
													<option value="viewer">{t('notes.shareRoleViewer')}</option>
													<option value="editor">{t('notes.shareRoleEditor')}</option>
												</select>
												<button
													class="button"
													disabled={!sharePick || shareBusy}
													onclick={addShare}
												>
													{t('notes.shareAdd')}
												</button>
											</div>
										{/if}
									</section>
								</div>
							{/if}
						</div>
					{/if}

					<button class="button" onclick={save}>{t('notes.save')}</button>
					{#if ownsSelected}
						<!-- Laid away, not thrown away: archive is the note leaving the list
						     without leaving the account, and it reads "bring it back" once it
						     already has. Owner only, like delete. -->
						<button class="button" onclick={() => archiveOne(selected)}>
							{selected.archived_at ? t('notes.unarchive') : t('notes.archive')}
						</button>
						<button class="button danger" onclick={() => remove(selected)}>{t('notes.delete')}</button>
					{/if}
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
		   ingen kant havde og ingenting kunne støde imod. Et par pixels mere om
		   notelisten, så kortet ikke ligger klods op ad menuen. */
		padding: var(--s3) var(--s4) var(--s3) calc(var(--s4) + 3px);
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
		/* A rounded card, the same corner and border as the editor beside it — the
		   list is its own sheet, not a column ruled off the page. */
		border: 1px solid var(--line);
		border-radius: var(--radius);
		/* White in light, a nuance lighter than the ground in dark — the note list
		   is a surface you read down, so it lifts off the page like the editor does.
		   --surface-raised is #fff in the light themes and one step up in the dark. */
		background: var(--surface-raised);
		padding: var(--s3);
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

	/* Kassen ruller. Listerne indeni gør ikke: de er mange nu, og et flex-element,
	   der både skrumper og klipper, viser ingenting. */
	.list {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
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
		contain-intrinsic-size: auto 74px;
	}

	/* A faint hairline between notes, as Apple Notes has — the group boxes give the
	   coarse structure, this separates one note from the next inside a group. */
	.rowline {
		display: flex;
		align-items: center;
		gap: var(--s1);
		border-bottom: 1px solid var(--line);
	}

	li:last-child .rowline {
		border-bottom: none;
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
	/* One design with the sidebar headings: a rounded box, the same corner as an
	   active menu item, filled just enough to separate one group of notes from the
	   next. The hard rule underneath is gone — the box is the separation now. */
	h3.group {
		position: sticky;
		top: 0;
		z-index: 1;
		margin: 0 0 var(--s1);
		background: var(--ground);
		border-radius: var(--radius);
	}

	/* Bold and plainly cased, in the reading ink — the faint uppercase label read as
	   a different app's heading beside the sidebar's. Menu size, so the two tiers of
	   heading in front of the eye at once are the same size. */
	h3.group button {
		display: flex;
		align-items: center;
		gap: var(--s2);
		width: 100%;
		padding: var(--s2) var(--s2);
		font-size: var(--menu-size);
		font-weight: 600;
		letter-spacing: normal;
		text-transform: none;
		color: var(--ink);
		border-radius: var(--radius);
	}

	h3.group button:hover {
		background: var(--surface-sunken);
	}

	/* The mark, or an empty box the same width where a date group has none, so every
	   label starts at the same place. The star and the people icon fill it; the
	   arrow that used to is gone — the box folds on a click. */
	h3.group .mark {
		flex: none;
		width: 0.75em;
		font-size: 0.85em;
		line-height: 1;
		color: var(--ink-faint);
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
		color: var(--ink-muted);
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
		background: var(--note-on-bg, var(--surface-raised));
		color: var(--note-on-ink, var(--ink));
	}

	/* On a themed selection colour (Kul's gold), the row's own quieter tiers go dark
	   too, or the date and the filed line vanish into it. Other themes have no
	   --note-on-ink, so these fall back to their normal muted inks. */
	.row.on .when,
	.row.on .filed {
		color: var(--note-on-ink, var(--ink-muted));
	}

	.row.on .filed-icon {
		color: var(--note-on-ink, var(--ink-faint));
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

	/* Datoen forrest på linje to og venstrestillet, som Apple Noter — ikke skubbet
	   ud til højre. Tabular-tal, så en søjle af datoer står på lige linjer. */
	.when {
		flex: none;
		color: var(--ink-muted);
		font-variant-numeric: tabular-nums;
	}

	/* Linje to er en flex-række: datoen, der ikke krymper, og uddraget, der klippes.
	   Afkortningen skal stå på uddraget selv — et inline-element klippes ikke af
	   `overflow: hidden`. */
	.preview {
		display: flex;
		align-items: baseline;
		gap: 0.45em;
		min-width: 0;
	}

	.snippet {
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
	/* Linje tre: hvor noten er lagt. En mappe som grupperne i menuen og et navn,
	   venstrestillet under uddraget — ikke en mærkat skubbet ud til højre. */
	.filed {
		display: flex;
		align-items: center;
		gap: 0.4em;
		min-width: 0;
		margin-top: 1px;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.filed-icon {
		width: 13px;
		height: 13px;
		flex: none;
		color: var(--ink-faint);
	}

	.filed-name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
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
		/* A nuance lighter than the ground in dark, still white in light — matches
		   the note list beside it so the two read as one writing surface. Kul gives
		   the text field its own darker grey via --editor-bg; other themes fall back
		   to the shared surface so the list and the editor stay the same colour. */
		background: var(--editor-bg, var(--surface-raised));
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
		border: 1px solid var(--line);
		border-radius: var(--radius-sm);
		padding: 0 var(--s1);
		font-family: var(--mono, ui-monospace, monospace);
		font-size: inherit;
		cursor: pointer;
	}
	.link:hover {
		color: var(--ink);
		background: var(--surface-raised);
		border-color: var(--line-strong);
	}

	/* Sharing lives in a popover above the Del button, so the footer stays a status
	   and a few buttons rather than a row that runs off its own width. */
	.sharewrap {
		position: relative;
		display: inline-flex;
	}

	.button.on {
		background: var(--surface-raised);
	}

	.sharewrap .count {
		margin-left: 0.4em;
		padding: 0 0.45em;
		border-radius: var(--radius-full);
		background: var(--line-strong);
		font-size: 0.85em;
	}

	.sharepanel {
		position: absolute;
		bottom: calc(100% + var(--s2));
		right: 0;
		z-index: 20;
		width: 20rem;
		max-width: 80vw;
		display: flex;
		flex-direction: column;
		gap: var(--s3);
		padding: var(--s3);
		background: var(--surface-raised);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		box-shadow: 0 8px 24px rgb(0 0 0 / 0.18);
		text-align: left;
		cursor: default;
	}

	.sharepanel section,
	.sharepanel .field {
		display: flex;
		flex-direction: column;
		gap: var(--s1);
	}

	.sharepanel .phead {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--ink-muted);
	}

	.sharepanel select {
		font-size: var(--text-xs);
		padding: 2px var(--s1);
	}

	/* The person icon on the Delt med mig heading, sized to the row of text it
	   sits in rather than to a fixed pixel. */
	h3.group .peoplemark {
		flex: none;
		width: 1em;
		height: 1em;
		color: var(--ink-faint);
	}
	h3.group .peoplemark svg {
		width: 100%;
		height: 100%;
		display: block;
	}


	.sharepanel .none {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.sharelist {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.sharelist li {
		display: flex;
		align-items: center;
		gap: var(--s1);
		font-size: var(--text-xs);
	}

	.sharelist .who {
		display: flex;
		align-items: center;
		gap: 0.375em;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.sharelist .avatar {
		width: 8px;
		height: 8px;
		flex: none;
		border-radius: var(--radius-full);
		background: var(--who, var(--line-strong));
	}

	.sharelist select {
		font-size: var(--text-xs);
		padding: 1px var(--s1);
		margin-left: auto;
	}

	/* The × that takes access away. A word would crowd the row; the mark is enough
	   beside a name and a role. */
	.unshare {
		flex: none;
		width: 1.3em;
		height: 1.3em;
		line-height: 1;
		border: none;
		background: transparent;
		color: var(--ink-faint);
		cursor: pointer;
		border-radius: var(--radius);
	}
	.unshare:hover {
		color: var(--danger, var(--ink));
		background: var(--surface-raised);
	}

	.addshare {
		display: flex;
		align-items: center;
		gap: var(--s1);
		flex-wrap: wrap;
		margin-top: var(--s1);
	}
	/* The person picker takes its own row, so the role and the button below it never
	   get squeezed to nothing in the panel's width. */
	.addshare select:first-child {
		flex: 1 1 100%;
	}
	.addshare .button {
		margin-left: auto;
	}
	.addshare select {
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

	/* The way back to the list on a phone — an iOS-style "‹ Noter" at the top of the
	   editor. Hidden on a wide screen, where the list is always beside the editor. */
	.back {
		display: none;
		align-self: flex-start;
		align-items: center;
		gap: var(--s1);
		margin-bottom: var(--s2);
		padding: var(--s1) 0;
		font-size: var(--text-base);
		color: var(--accent);
	}

	@media (max-width: 700px) {
		.notes {
			grid-template-columns: 1fr;
			padding: 0;
			gap: 0;
		}

		/* One pane at a time. The two-column card layout has no room on a phone, so
		   the list shows until a note is opened and the editor takes over once one is
		   — the back button is the way between them. Without this the editor stacked
		   below a full-height list, so a tapped note opened out of sight. */
		.notes.has-note aside {
			display: none;
		}

		.notes:not(.has-note) .editor {
			display: none;
		}

		/* Full-bleed: the card border and rounded corners are for sitting beside
		   another card, and on one column there is nothing beside it. */
		aside,
		.editor {
			border: 0;
			border-radius: 0;
		}

		.back {
			display: inline-flex;
		}
	}
</style>
