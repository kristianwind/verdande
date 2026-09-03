<script>
	/**
	 * A calendar grid: a whole month, or a single week.
	 *
	 * Weeks start on Monday. That is not a preference — it is ISO 8601 and it is
	 * what every Danish calendar does, and a Sunday-first grid makes a Dane
	 * misread the whole month at a glance.
	 *
	 * One component for both spans rather than two, because the part that is easy
	 * to get subtly wrong is the same in both: the drag target has to decide from
	 * `dataTransfer.types` alone whether it accepts a drop, and a second copy of
	 * that is a second place for it to be almost right.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { TASK, startDrag, carries, dragged, accept } from '$lib/dnd.js';
	import { t, tag } from '$lib/i18n.svelte.js';

	let {
		/**
		 * What a chip does when clicked. Opening the task is what it should do
		 * everywhere, so it is the default rather than something each caller has to
		 * remember — the project page did not, and its chips were dead for as long
		 * as the month view has existed.
		 */
		onselect = (task) => app.openDetail(task.id),
		/**
		 * Told which dates the grid is showing, whenever that changes.
		 *
		 * A project's month view needs nothing: the page has already loaded every
		 * task in the project. Kommende is the other case — it cannot load every
		 * task anybody has ever dated — so it asks for the month on screen, and
		 * this is how it learns which one that is.
		 */
		onrange,
		/**
		 * Narrows the grid to one project. The store holds whatever the last view
		 * loaded, so a project's month has to say which project it is — Kommende,
		 * which shows every project, is the one that leaves this out.
		 */
		projectId = null,
		/**
		 * 'month' for the whole month as six weeks, 'week' for one week as a single
		 * row.
		 *
		 * The week exists for dragging across a month boundary. A month grid is
		 * anchored to a month, so the two days on either side of its edge are in
		 * different grids and moving a task between them means changing month
		 * mid-drag — which the browser will not let you do, because a drag in
		 * flight swallows the click that would page. A week that straddles the
		 * 31st and the 1st has both days in the same row.
		 */
		span = 'month',
		/**
		 * Events read from somewhere else, laid over the same grid.
		 *
		 * An event is not a task, and the cell has to say so. It has a span of time
		 * rather than a day, it cannot be ticked off, and — while the connection is
		 * read-only — it cannot be moved: verdande holds a copy of somebody else's
		 * calendar, and a chip that lets itself be dragged is a promise the server
		 * has no way to keep.
		 *
		 * Empty everywhere but Kalender, so no other view pays for it.
		 */
		events = []
	} = $props();

	// Sat til i dag og lagt til rette af effekten nedenfor.
	//
	// Ikke `span === 'week' ? … : …` her: den læser `span`, som den er i det
	// øjeblik komponenten bliver til, og fanger den værdi for altid — hvilket
	// byggeriet har advaret om lige så længe. Så længe hver visning havde sin egen
	// komponent, gjorde det ingen forskel; det gjorde det i samme sekund, et
	// projekts kalender fik en uge/måned-vælger, der skifter `span` på den samme.
	let cursor = $state(new Date());

	/**
	 * The weekday names, from Intl rather than written out.
	 *
	 * Not to save seven strings — so the abbreviations are the ones the language
	 * actually uses. Every language shortens its weekdays differently, and guessing
	 * at that from a translation table is how a calendar ends up saying something
	 * no reader of that language would write.
	 *
	 * Monday first, because the grid is. The 1st of January 2024 was a Monday; any
	 * Monday would do, and a constant one keeps this out of the render path.
	 */
	const WEEKDAYS = $derived.by(() => {
		const format = new Intl.DateTimeFormat(tag(), { weekday: 'short' });
		return Array.from({ length: 7 }, (_, i) => format.format(new Date(Date.UTC(2024, 0, 1 + i))));
	});

	function startOfMonth(d) {
		return new Date(d.getFullYear(), d.getMonth(), 1);
	}

	// getDay() is Sunday-based; this converts to "days since Monday".
	function startOfWeek(d) {
		const start = new Date(d.getFullYear(), d.getMonth(), d.getDate());
		start.setDate(start.getDate() - ((start.getDay() + 6) % 7));
		return start;
	}

	function startOfSpan(d) {
		return span === 'week' ? startOfWeek(d) : startOfMonth(d);
	}

	// The cursor has to follow the span it is measured in: switching from month to
	// week with a cursor on the 1st would otherwise show the week containing the
	// 1st rather than the week you were looking at, and switching back would land
	// on whichever month that week happened to start in.
	//
	// Med én undtagelse, og den er hele grunden til at det her er en effekt og ikke
	// en udregning: en månedsvisnings markør *er* den første i måneden. Skifter man
	// til uge, mens man står i den indeværende måned, er den uge man kigger på ikke
	// ugen omkring den 1. — det er den, i dag ligger i. Uden det her landede et
	// projekts kalender på "27. jul.–2. aug." for en person, der kiggede på august.
	$effect(() => {
		const anchor =
			span === 'week' && isMonthStart(cursor) && sameMonth(cursor, new Date())
				? new Date()
				: cursor;
		const aligned = startOfSpan(anchor);
		// Compared by value, not identity: assigning a fresh Date on every run would
		// re-trigger this effect forever, since the effect reads what it writes.
		if (aligned.getTime() !== cursor.getTime()) cursor = aligned;
	});

	const isMonthStart = (d) => d.getDate() === 1;
	const sameMonth = (a, b) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth();

	function iso(d) {
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
			d.getDate()
		).padStart(2, '0')}`;
	}

	// A month always runs whole weeks, so it starts on the Monday on or before the
	// first and ends on the Sunday on or after the last. A week is already one.
	let grid = $derived.by(() => {
		const start = span === 'week' ? startOfWeek(cursor) : startOfWeek(startOfMonth(cursor));
		// Six weeks covers every possible month layout, including a 31-day month
		// that begins on a Sunday.
		const length = span === 'week' ? 7 : 42;

		const days = [];
		const cell = new Date(start);
		for (let i = 0; i < length; i++) {
			days.push(new Date(cell));
			cell.setDate(cell.getDate() + 1);
		}
		return days;
	});

	// Reported after the grid is worked out rather than from the cursor, because
	// the grid runs whole weeks: a month view nearly always shows a few days of the
	// month before it and a few of the one after, and a caller loading only the
	// month itself would leave those corners empty.
	$effect(() => {
		onrange?.({ from: iso(grid[0]), to: iso(grid[grid.length - 1]) });
	});

	const todayISO = iso(new Date());

	const tasksOn = (date) =>
		app.tasks.filter(
			(t) =>
				!t.completed &&
				t.due_date === iso(date) &&
				(!projectId || t.project_id === projectId)
		);

	/**
	 * Which cells an event covers, rather than where it starts. A trip from Friday
	 * to Monday is in all four, and asking only about the start day would draw it
	 * once and leave three days looking clear.
	 *
	 * Ordered here rather than trusted from the caller. The server does sort, but a
	 * cell that shows 14:00 above 09:15 does not look like a list in the wrong
	 * order — it looks like the clock on the chip is wrong, which is a far worse
	 * thing to have to rule out. All-day first, because an all-day event is a band
	 * over the whole day rather than something that happens at a point in it.
	 */
	const eventsOn = (date) => {
		const day = iso(date);
		return events
			.filter((e) => e.start_day <= day && e.end_day >= day)
			.toSorted((a, b) => {
				if (a.all_day !== b.all_day) return a.all_day ? -1 : 1;
				return (a.starts_at ?? '').localeCompare(b.starts_at ?? '');
			});
	};

	/**
	 * The time on the chip, and only where it means something.
	 *
	 * An all-day event has no time. A timed one shows its start, and only on the
	 * day it starts: a meeting that runs past midnight would otherwise claim to
	 * begin at 20:00 on both days.
	 *
	 * The clock is read out of the string rather than parsed into a Date. Google
	 * writes the event's own offset into it, and handing that to the browser asks
	 * *this device* what time it is — so a phone in another time zone would show a
	 * Copenhagen meeting an hour out.
	 */
	function timeOn(event, date) {
		if (event.all_day || !event.starts_at) return '';
		if (event.start_day !== iso(date)) return '';
		return event.starts_at.slice(11, 16);
	}

	/**
	 * The calendar's own colour, or nothing.
	 *
	 * Checked rather than trusted, even though it comes from Google: this goes into
	 * an inline `style`, and a value that is not a colour is a value that can close
	 * the declaration and open another. A hex triple is the whole of what Google
	 * sends, so anything else is not a colour that failed to render — it is a
	 * string that has no business being here.
	 */
	const HEX = /^#[0-9a-f]{3,8}$/i;
	const swatch = (colour) => (HEX.test(colour ?? '') ? colour : '');

	/**
	 * Whether the event links back to Google.
	 *
	 * `https:` and nothing else. An href is where a `javascript:` URL becomes a
	 * click that runs, and the address in this field was written by a service, not
	 * by the person looking at it.
	 */
	const linkOf = (event) => (String(event.url ?? '').startsWith('https://') ? event.url : '');

	const monthName = $derived(
		cursor.toLocaleDateString(tag(), { month: 'long', year: 'numeric' })
	);

	// "24. aug. – 30. aug. 2026", collapsed to one month name when the week does not
	// straddle two. The year is said once, at the end, where it is least in the way.
	const weekName = $derived.by(() => {
		const from = grid[0];
		const to = grid[grid.length - 1];
		const day = { day: 'numeric', month: 'short' };
		const left =
			from.getMonth() === to.getMonth()
				? from.toLocaleDateString(tag(), { day: 'numeric' })
				: from.toLocaleDateString(tag(), day);
		return `${left}–${to.toLocaleDateString(tag(), day)} ${to.getFullYear()}`;
	});

	/**
	 * The ISO week number, which is what a Dane means by "uge 35".
	 *
	 * Counted from the Thursday of the week, because that is the definition: week 1
	 * is the one containing the first Thursday of the year, so the Thursday is the
	 * only day guaranteed to be in the same year as the week it belongs to.
	 */
	function isoWeek(d) {
		const thursday = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()));
		thursday.setUTCDate(thursday.getUTCDate() - ((thursday.getUTCDay() + 6) % 7) + 3);
		const first = new Date(Date.UTC(thursday.getUTCFullYear(), 0, 4));
		first.setUTCDate(first.getUTCDate() - ((first.getUTCDay() + 6) % 7) + 3);
		return 1 + Math.round((thursday - first) / (7 * 86400000));
	}

	const heading = $derived(span === 'week' ? weekName : monthName);
	const weekNumber = $derived(span === 'week' ? isoWeek(grid[0]) : null);

	// A week shows seven cells where a month shows forty-two, so each one has the
	// height to be a list rather than a hint. Truncating it to three there would
	// hide work in the empty half of a tall cell.
	const chipLimit = $derived(span === 'week' ? 10 : 3);

	// One step is one span: a month view pages by month, a week view by week. A week
	// view that paged by month would be back to the problem it exists to solve.
	function step(n) {
		if (span === 'week') {
			const next = new Date(cursor);
			next.setDate(next.getDate() + n * 7);
			cursor = next;
			return;
		}
		cursor = new Date(cursor.getFullYear(), cursor.getMonth() + n, 1);
	}

	// --- dropping a task on a day ---------------------------------------------------

	/** The cell lit up under the pointer. */
	let over = $state(null);

	// A resize in progress: which task, and where its foot is being dragged to, in
	// minutes since midnight. Kept apart from the stored length so the task can
	// follow the handle live and only be written when the drag ends.
	let resizing = $state(null);

	/**
	 * Dragging the foot of a timed task changes how long it is.
	 *
	 * Pointer events rather than the HTML5 drag the body uses: the two must not be
	 * the same gesture, or every attempt to make a task longer would instead pick it
	 * up and move it. stopPropagation keeps the handle's press from starting the
	 * move, and the column it lives in is what the minutes are measured against —
	 * the same box onDropAt reads a drop time from.
	 */
	function startResize(event, task, date) {
		event.stopPropagation();
		event.preventDefault();
		const col = event.currentTarget.closest('.daycol');
		if (!col || !task.due_datetime) return;
		const at = new Date(task.due_datetime);
		const from = at.getHours() * 60 + at.getMinutes();
		resizing = { id: task.id, from, to: from + (task.duration_min || 30), date: iso(date) };

		const onMove = (e) => {
			const box = col.getBoundingClientRect();
			const share = Math.min(Math.max((e.clientY - box.top) / box.height, 0), 1);
			const minutes = hours.from * 60 + share * hours.minutes;
			// Quarter-hour steps, at least a quarter long, never past midnight.
			const snapped = Math.min(Math.max(Math.round(minutes / 15) * 15, from + 15), 24 * 60);
			resizing = { ...resizing, to: snapped };
		};
		const onUp = async () => {
			window.removeEventListener('pointermove', onMove);
			window.removeEventListener('pointerup', onUp);
			const current = resizing;
			resizing = null;
			if (current) await app.resize(current.id, current.to - current.from);
		};
		window.addEventListener('pointermove', onMove);
		window.addEventListener('pointerup', onUp);
	}

	function onDragOver(event, date) {
		if (!carries(event, TASK)) return;
		accept(event);
		over = iso(date);
	}

	/**
	 * Sluppet på en dag, uden at sige noget om klokkeslættet — så beholder opgaven
	 * det, den har. Måneden er den her: en rude er en dag, ikke et døgn, og der er
	 * ingen tid at læse ud af hvor man slap.
	 *
	 * Det var ikke sådan før. Trækket sendte kun datoen, og serveren læser en dato
	 * uden en tid som "ryd tiden" — så en opgave klokken 14 mistede sit
	 * klokkeslæt ved at blive flyttet til dagen efter, uden at nogen bad om det.
	 */
	async function onDrop(event, date) {
		event.preventDefault();
		const id = dragged(event, TASK);
		over = null;
		if (!id) return;
		await app.reschedule(id, iso(date));
	}

	/**
	 * Sluppet et sted på døgnet: dér, man slap, er klokkeslættet.
	 *
	 * Målt mod søjlens egen kasse frem for mod siden, så det bliver rigtigt, uanset
	 * hvor gitteret sidder, og uanset hvor langt der er rullet.
	 *
	 * Rundet til kvarter. Minuttet under markøren er ikke en oplysning, nogen har —
	 * pegefingeren er bredere end et minut — og 14:07 er et tal, der ser ud som om
	 * det blev valgt, hvilket er værre end at være omtrentligt.
	 */
	async function onDropAt(event, date) {
		event.preventDefault();
		const id = dragged(event, TASK);
		over = null;
		if (!id) return;

		const box = event.currentTarget.getBoundingClientRect();
		const share = Math.min(Math.max((event.clientY - box.top) / box.height, 0), 1);
		const minutes = hours.from * 60 + share * hours.minutes;
		const snapped = Math.min(Math.round(minutes / 15) * 15, 24 * 60 - 15);
		await app.reschedule(id, iso(date), clock(snapped));
	}

	/**
	 * Sluppet i båndet foroven: opgaven har en dag og ingen tid.
	 *
	 * Den tomme streng er ikke det samme som ingenting her — den siger "ryd", hvor
	 * `undefined` siger "behold". Det er hele grunden til, at de to er skilt ad i
	 * `reschedule`.
	 */
	async function onDropAllDay(event, date) {
		event.preventDefault();
		const id = dragged(event, TASK);
		over = null;
		if (!id) return;
		await app.reschedule(id, iso(date), '');
	}

	// --- ugen som et døgn ---------------------------------------------------------
	//
	// En uge, hvor hver dag er en punktopstilling, svarer på "hvad skal der ske
	// torsdag" og ikke på "hvornår" — og det andet er det, man åbner en uge for.
	// To møder klokken ti er en konflikt, man skal kunne *se*, ikke læse sig til.
	//
	// Måneden bliver som den er. En dag i en månedsrude er halvanden centimeter høj;
	// et døgn tegnet i den er ikke en visning, det er en streg.

	/** Minutter siden midnat, læst ud af strengen. */
	const minutesAt = (stamp) => Number(stamp.slice(11, 13)) * 60 + Number(stamp.slice(14, 16));

	const clock = (minutes) =>
		`${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;

	/**
	 * Det, der har et klokkeslæt på denne dag, med start og slut i minutter.
	 *
	 * Begivenheder læses som tekst, af samme grund som `timeOn` gør det: Google
	 * skriver begivenhedens egen forskydning ind i stemplet, og at give det til
	 * browseren spørger *denne maskine*, hvad klokken er. Opgaver læses med `Date`,
	 * fordi det er sådan TaskRow gør det ét skærmbillede væk — en opgaves tid er
	 * kontoens egen, og de to steder skal vise det samme tal.
	 *
	 * En begivenhed hen over midnat klippes til dagen. Uden det ville et møde fra
	 * fredag aften til lørdag morgen blive tegnet fra 20:00 til 09:00, altså baglæns.
	 */
	function timedOn(date) {
		const day = iso(date);
		const out = [];

		for (const event of events) {
			if (event.all_day || !event.starts_at) continue;
			if (event.start_day > day || event.end_day < day) continue;
			const from = event.start_day === day ? minutesAt(event.starts_at) : 0;
			let to = 24 * 60;
			if (event.ends_at && event.end_day === day) to = minutesAt(event.ends_at);
			else if (!event.ends_at) to = from + 60;
			// Et kvarters gulv: et nulminuts møde er stadig noget, der skal kunne ses
			// og klikkes på.
			out.push({ kind: 'event', ref: event, from, to: Math.max(to, from + 15) });
		}

		for (const task of tasksOn(date)) {
			if (!task.due_datetime) continue;
			const at = new Date(task.due_datetime);
			const from = at.getHours() * 60 + at.getMinutes();
			// The task's own length, not a fixed half hour. A quarter-hour floor so a
			// zero-minute task is still something to see and grab, and the end of the
			// day is the ceiling. While a resize is in flight this task follows the
			// handle instead of its stored length.
			let to =
				resizing && resizing.id === task.id
					? resizing.to
					: from + (task.duration_min || 30);
			to = Math.min(Math.max(to, from + 15), 24 * 60);
			out.push({ kind: 'task', ref: task, from, to });
		}

		return out.toSorted((a, b) => a.from - b.from || a.to - b.to);
	}

	/** Det uden klokkeslæt: heldagsbegivenheder og opgaver, der kun har en dato. */
	function untimedOn(date) {
		return [
			...eventsOn(date)
				.filter((e) => e.all_day || !e.starts_at)
				.map((e) => ({ kind: 'event', ref: e })),
			...tasksOn(date)
				.filter((t) => !t.due_datetime)
				.map((t) => ({ kind: 'task', ref: t }))
		];
	}

	/**
	 * To ting på samme tid står ved siden af hinanden, ikke oven på hinanden.
	 *
	 * Sammenhængende klynger frem for parvise sammenligninger: overlapper A med B og
	 * B med C, skal alle tre dele bredden — også når A og C ikke rører hinanden.
	 * Ellers ville A og C hver tro, de havde halvdelen, og lægge sig oven på hver sin
	 * halvdel af B.
	 */
	function packed(items) {
		const out = [];
		let cluster = [];
		let end = -1;

		const flush = () => {
			const lanes = [];
			for (const item of cluster) {
				let lane = lanes.findIndex((free) => free <= item.from);
				if (lane === -1) {
					lane = lanes.length;
					lanes.push(0);
				}
				lanes[lane] = item.to;
				item.lane = lane;
			}
			for (const item of cluster) item.lanes = lanes.length;
			out.push(...cluster);
			cluster = [];
			end = -1;
		};

		for (const item of items) {
			if (cluster.length && item.from >= end) flush();
			cluster.push(item);
			end = Math.max(end, item.to);
		}
		if (cluster.length) flush();
		return out;
	}

	/**
	 * Hvilke timer der tegnes.
	 *
	 * Et helt døgn er fireogtyve rækker, hvoraf de otte altid er tomme, og så er
	 * gitteret noget man ruller i frem for noget man ser. Arbejdsdagen er gulvet, og
	 * ugens egne begivenheder skubber kanterne ud, når de ligger uden for den — en
	 * vagt klokken fem om morgenen kommer med, uden at alle andre uger betaler for
	 * den med tomme rækker.
	 */
	let hours = $derived.by(() => {
		let from = 8;
		let to = 18;
		if (span === 'week') {
			for (const date of grid) {
				for (const item of timedOn(date)) {
					from = Math.min(from, Math.floor(item.from / 60));
					to = Math.max(to, Math.ceil(item.to / 60));
				}
			}
		}
		from = Math.max(0, from);
		to = Math.min(24, Math.max(to, from + 1));
		return { from, to, count: to - from, minutes: (to - from) * 60 };
	});

	/** Hvor på døgnet noget står, som procent af de timer, der tegnes. */
	function place(item) {
		const top = Math.max(0, ((item.from - hours.from * 60) / hours.minutes) * 100);
		const height = ((item.to - item.from) / hours.minutes) * 100;
		const width = 100 / item.lanes;
		return (
			`top:${top}%;height:${Math.min(height, 100 - top)}%;` +
			`left:${item.lane * width}%;width:${width}%`
		);
	}
</script>

<div class="calendar" class:week={span === 'week'}>
	<header>
		<button onclick={() => step(-1)} aria-label={span === 'week' ? t('view.prevWeek') : t('view.prevMonth')}
			>‹</button
		>
		<h2 class:week={span === 'week'}>
			{heading}
			{#if weekNumber}<span class="weekno">{t('view.weekNumber', { n: weekNumber })}</span>{/if}
		</h2>
		<button onclick={() => step(1)} aria-label={span === 'week' ? t('view.nextWeek') : t('view.nextMonth')}
			>›</button
		>
		<button class="today" onclick={() => (cursor = startOfSpan(new Date()))}>{t('task.today')}</button>
	</header>

	<!--
		Én brik, tegnet ét sted.

		Måneden og ugen viser de samme to ting og skal vise dem ens — men de placerer
		dem forskelligt: måneden lader dem stå i rækkefølge i en rude, ugen sætter
		dem på et klokkeslæt. Det er placeringen, der er forskellig, ikke brikken, så
		det er kun placeringen, der står to steder.

		`extra` er den absolutte placering i ugegitteret og tom i måneden. `at` er
		klokkeslættet, når det ikke allerede står i brikken selv.
	-->
	{#snippet eventChip(event, date, kind, extra = '', at = '')}
		{@const link = linkOf(event)}
		{@const label = t('cal.eventCannotMove', {
			name: event.summary,
			calendar: event.calendar_name || t('cal.title')
		})}
		<!-- Not draggable, and it says so twice: the attribute stops the browser
		     dragging an anchor of its own accord, and the title says why to anybody
		     who tries. The drop targets would refuse it anyway — they only accept the
		     task MIME type — but a chip that lifts off the page and then will not land
		     reads as a bug rather than as a rule. It is also not a button: there is
		     nothing here to complete, and a checkbox next to a meeting is an offer
		     verdande cannot keep. -->
		<svelte:element
			this={link ? 'a' : 'span'}
			class={kind}
			class:allday={event.all_day}
			draggable="false"
			href={link || undefined}
			target={link ? '_blank' : undefined}
			rel={link ? 'noreferrer noopener' : undefined}
			title={link ? `${label} — ${t('cal.openInGoogle')}` : label}
			style={[swatch(event.colour) ? `--event-colour: ${swatch(event.colour)}` : '', extra]
				.filter(Boolean)
				.join(';')}
		>
			{#if at}<span class="at">{at}</span>{:else if timeOn(event, date)}<span class="at"
					>{timeOn(event, date)}</span
				>{/if}
			{event.summary}
		</svelte:element>
	{/snippet}

	{#snippet taskChip(task, kind, extra = '', at = '', date = null)}
		<!-- Draggable as well as clickable: moving something to another day is the
		     whole reason to be looking at a calendar. The browser needs a few pixels
		     of movement before it calls a press a drag, so the click survives. -->
		<button
			class={kind}
			class:resizing={resizing?.id === task.id}
			data-priority={task.priority}
			draggable="true"
			ondragstart={(e) => startDrag(e, TASK, task.id)}
			ondragend={() => (over = null)}
			onclick={() => onselect?.(task)}
			style={extra}
		>
			{#if at}<span class="at">{at}</span>{/if}
			{task.content}
			<!-- Only a task standing at a time can be made longer, and only where the
			     grid has room to show it — the timed column, which passes the date. -->
			{#if date && task.due_datetime}
				<span
					class="resize"
					title={t('cal.resize')}
					aria-hidden="true"
					onpointerdown={(e) => startResize(e, task, date)}
				></span>
			{/if}
		</button>
	{/snippet}

	{#if span === 'week'}
		<!-- Ugen som et døgn, ikke som syv punktopstillinger.
		     Rullet vandret på en smal skærm frem for stablet til én søjle: der er
		     ét sæt afleveringsmål her, og et andet sæt til telefonen ville være
		     endnu et sted, den samme regel kunne være næsten rigtig. -->
		<div class="weekwrap">
			<div class="weekgrid" style="--hours: {hours.count}">
				<div class="corner"></div>
				{#each grid as date (date.toISOString())}
					<div class="head" class:today={iso(date) === todayISO}>
						<span class="wd">{WEEKDAYS[(date.getDay() + 6) % 7]}</span>
						<span class="num">{date.getDate()}</span>
					</div>
				{/each}

				<!-- Båndet foroven: det, der ikke har et klokkeslæt og derfor ikke kan
				     stå ét sted på døgnet. En heldagsbegivenhed lagt klokken nul ville
				     påstå, den holdt fra midnat til midnat. -->
				<div class="gutter">{t('cal.allDay')}</div>
				{#each grid as date (date.toISOString())}
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<div
						class="allday"
						data-allday={iso(date)}
						class:today={iso(date) === todayISO}
						class:over={over === iso(date)}
						ondragover={(e) => onDragOver(e, date)}
						ondragleave={() => (over = null)}
						ondrop={(e) => onDropAllDay(e, date)}
					>
						{#each untimedOn(date) as item (item.kind + item.ref.id)}
							{#if item.kind === 'event'}
								{@render eventChip(item.ref, date, 'event')}
							{:else}
								{@render taskChip(item.ref, 'chip')}
							{/if}
						{/each}
					</div>
				{/each}

				<div class="gutter times">
					{#each Array(hours.count) as _, i}
						<span class="hour">{clock((hours.from + i) * 60)}</span>
					{/each}
				</div>
				{#each grid as date (date.toISOString())}
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<!-- `data-date` er dagens navn udadtil. Månedens ruder har altid haft
					     det, og ugens søjler skal have det af samme grund: det er sådan
					     noget uden for komponenten peger på en dag — og det er det, en
					     prøve slipper en opgave på. Heldagsbåndet får sit eget navn, så
					     de to ikke bliver til det samme svar på ét spørgsmål. -->
					<div
						class="daycol"
						data-date={iso(date)}
						class:today={iso(date) === todayISO}
						class:over={over === iso(date)}
						ondragover={(e) => onDragOver(e, date)}
						ondragleave={() => (over = null)}
						ondrop={(e) => onDropAt(e, date)}
					>
						{#each Array(hours.count) as _, i}
							<div class="hourline" style="top:{(i / hours.count) * 100}%"></div>
						{/each}
						{#each packed(timedOn(date)) as item (item.kind + item.ref.id)}
							{#if item.kind === 'event'}
								{@render eventChip(item.ref, date, 'tevent', place(item))}
							{:else}
								{@render taskChip(item.ref, 'tevent task', place(item), clock(item.from), date)}
							{/if}
						{/each}
					</div>
				{/each}
			</div>
		</div>
	{:else}
		<div class="weekdays" aria-hidden="true">
			{#each WEEKDAYS as day}
				<span>{day}</span>
			{/each}
		</div>

		<div class="grid">
			{#each grid as date (date.toISOString())}
				{@const dayEvents = eventsOn(date)}
				<!-- Events first, then whatever room is left goes to tasks. An event is
				     fixed to a time somebody else chose; a task is only dated, and if
				     something in a full cell has to fall behind "+3 mere", it should be
				     the one that can be moved. -->
				{@const tasks = tasksOn(date).slice(0, Math.max(0, chipLimit - dayEvents.length))}
				{@const hidden =
					tasksOn(date).length - tasks.length + Math.max(0, dayEvents.length - chipLimit)}
				<!-- A week has no outside: every day in it is the week you asked for. -->
				{@const outside = span !== 'week' && date.getMonth() !== cursor.getMonth()}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div
					class="day"
					data-date={iso(date)}
					class:outside
					class:today={iso(date) === todayISO}
					class:over={over === iso(date)}
					ondragover={(e) => onDragOver(e, date)}
					ondragleave={() => (over = null)}
					ondrop={(e) => onDrop(e, date)}
				>
					<!-- The weekday strip above the grid is enough on a wide screen. A week
					     stacked into rows on a phone hides it, and a column of bare numbers
					     is not a week — so the cell carries its own name there. -->
					{#if span === 'week'}
						<span class="weekday">{WEEKDAYS[(date.getDay() + 6) % 7]}</span>
					{/if}
					<span class="number">{date.getDate()}</span>

					{#each dayEvents.slice(0, chipLimit) as event (event.id)}
						{@render eventChip(event, date, 'event')}
					{/each}

					{#each tasks as task (task.id)}
						{@render taskChip(task, 'chip')}
					{/each}

					{#if hidden > 0}
						<span class="more">{t('view.more', { n: hidden })}</span>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	header {
		display: flex;
		align-items: center;
		gap: var(--s2);
		margin-bottom: var(--s4);
	}

	h2 {
		font-size: var(--text-lg);
		text-transform: capitalize;
		min-width: 180px;
	}

	/* A date range is not a month name: "24.–30. aug. 2026" is already capitalised
	   where it should be, and capitalising it again gives "24.–30. Aug. 2026". */
	h2.week {
		text-transform: none;
		min-width: 220px;
		display: flex;
		align-items: baseline;
		gap: var(--s2);
	}

	.weekno {
		font-size: var(--text-xs);
		font-weight: 400;
		color: var(--ink-faint);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	header button {
		width: 28px;
		height: 28px;
		display: grid;
		place-items: center;
		border-radius: var(--radius);
		color: var(--ink-muted);
		font-size: var(--text-lg);
		line-height: 1;
	}

	header button:hover {
		background: var(--surface);
		color: var(--ink);
	}

	/* Scoped to the header, because `.today` is not only this button: the day cell
	   for today carries the same class, and an unscoped `margin-left: auto` made a
	   grid item shrink to its content and sit against the right of its column. The
	   month always had one cell narrower than the other six, on every project
	   calendar, and it read as a grid that had not quite laid out yet. */
	header .today {
		width: auto;
		margin-left: auto;
		padding: 0 var(--s3);
		font-size: var(--text-sm);
		border: 1px solid var(--line);
	}

	/* `minmax(0, 1fr)` og ikke `1fr`.
	 *
	 * `1fr` er `minmax(auto, 1fr)`, og `auto` som minimum betyder, at en søjle ikke
	 * kan blive smallere end sit længste ord. Én opgave, der hedder "Gårdbutikken på
	 * Møllerup Gods — opfølgning på mail", gjorde onsdag dobbelt så bred som mandag
	 * og klemte resten af ugen sammen om den. Syv dage er syv lige store dage; det
	 * er hele idéen med et gitter.
	 *
	 * Nul som minimum lader søjlen blive så smal, den skal — og så er det brikkens
	 * opgave at klippe sin tekst af, hvilket den allerede gør. */
	.weekdays {
		display: grid;
		grid-template-columns: repeat(7, minmax(0, 1fr));
		gap: 1px;
		margin-bottom: var(--s2);
	}

	.weekdays span {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		padding: 0 var(--s2);
	}

	.grid {
		display: grid;
		grid-template-columns: repeat(7, minmax(0, 1fr));
		gap: 1px;
		background: var(--line);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	/* --- ugen som et døgn ---------------------------------------------------
	 *
	 * Et gitter med en tidsrende og syv dage, og tre rækker: dagenes navne, båndet
	 * med det, der ikke har et klokkeslæt, og selve døgnet.
	 *
	 * Rullet vandret på en smal skærm frem for stablet til én søjle. Syv timeglas
	 * ved siden af hinanden kan ikke blive smalle nok til en telefon, og en uge
	 * stablet til en liste er præcis det, den her visning findes for at holde op
	 * med at være. */
	.weekwrap {
		overflow-x: auto;
	}

	.weekgrid {
		display: grid;
		grid-template-columns: 3.25rem repeat(7, minmax(0, 1fr));
		gap: 1px;
		min-width: 640px;
		background: var(--line);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow: hidden;
	}

	/* White, not the sidebar's sunken ground: a calendar reads lightest when the
	   day itself is the brightest thing on the screen and the events are the soft
	   colour laid on it — the opposite of the grey grid it was, where the events
	   had to shout to be seen at all. */
	.corner,
	.head,
	.gutter,
	.allday,
	.daycol {
		background: var(--surface);
	}

	.head {
		padding: var(--s2) var(--s1);
		text-align: center;
	}

	/* Today's whole column, faintly, so the eye finds it before it reads a number —
	   the marker on the date above is the label, this is the glow it sits in. Kept
	   under the events, so a pastel block still reads as its own calendar's colour
	   rather than as this wash tinted. */
	.head.today,
	.allday.today,
	.daycol.today {
		background: color-mix(in oklab, var(--accent) 6%, var(--surface));
	}

	.head .wd {
		display: block;
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
	}

	.head .num {
		font-size: var(--text-sm);
	}

	/* The marker at the top the day is named by: today's number in a filled disc,
	   the same one the month grid gives today, so "this is today" is one glance in
	   either view rather than a colour you have to already know to read. */
	.head.today .num {
		color: var(--accent-ink);
		background: var(--accent);
		border-radius: var(--radius-full);
		width: 20px;
		height: 20px;
		display: inline-grid;
		place-items: center;
		font-weight: 560;
		margin-top: 1px;
	}

	.gutter {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		padding: var(--s1) var(--s2);
		font-size: var(--text-xs);
		line-height: 1.2;
		color: var(--ink-faint);
		text-align: right;
	}

	.allday {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: var(--s1);
		min-height: 1.75rem;
	}

	/* Rækken er lige så høj som de timer, den viser. `--hours` sættes på gitteret,
	   fordi tallet kommer fra ugens egne begivenheder og ikke fra stilarket. */
	.times {
		display: grid;
		align-items: start;
		justify-content: stretch;
		grid-template-rows: repeat(var(--hours), 1fr);
		height: calc(var(--hours) * 3rem);
		padding: 0 var(--s2) 0 0;
	}

	/* Klokkeslættet står ved sin egen streg og ikke midt i timen under den. */
	.times .hour {
		transform: translateY(-0.55em);
		line-height: 1;
	}

	/* Undtagen det første, som ellers kravler op i heldagsbåndet: der er ingen streg
	   over den første time — der er gitterets kant, og et tal, der ligger oven på
	   den, ser ud til at høre til rækken ovenover. */
	.times .hour:first-child {
		transform: none;
	}

	.daycol {
		position: relative;
		height: calc(var(--hours) * 3rem);
	}

	.hourline {
		position: absolute;
		left: 0;
		right: 0;
		height: 1px;
		background: var(--line);
		pointer-events: none;
	}

	/* Den første streg er gitterets egen kant. */
	.hourline:first-child {
		display: none;
	}

	.allday.over,
	.daycol.over {
		background: var(--accent-sunken, var(--surface-raised));
		box-shadow: inset 0 0 0 1px var(--accent);
	}

	/* Placeringen kommer fra `place()` som en inline `style`; alt andet står her.
	   To ting på samme tid deler bredden, så `margin-right` er den ene hårstrå
	   luft, der gør to kasser til to kasser. */
	/* A soft wash of the calendar's own colour rather than a grey block edged in it:
	   each calendar keeps a dusty, unmistakable nuance, and a screen of them reads
	   as colour laid on white instead of a grid of grey bars. The left edge stays,
	   a shade stronger than the fill, so the block still has a spine. */
	.tevent {
		position: absolute;
		margin-right: 2px;
		overflow: hidden;
		padding: 1px var(--s1);
		border-radius: var(--radius-sm);
		border-left: 3px solid var(--event-colour, var(--line-strong));
		background: color-mix(in oklab, var(--event-colour, var(--line-strong)) 16%, var(--surface));
		color: var(--ink);
		font-size: var(--text-xs);
		line-height: 1.3;
		text-align: left;
		text-decoration: none;
	}

	/* A task is a card you can pick up, not an event you cannot — so on the white
	   column it is an outlined card, where an event is a solid wash. The priority
	   colour rides the left edge, as it does everywhere else. */
	.tevent.task {
		background: var(--surface);
		box-shadow: inset 0 0 0 1px var(--line);
		border-left-color: var(--line-strong);
		color: var(--ink);
		cursor: grab;
	}

	/* The grip along the foot of a task, for dragging its length. Out of the way
	   until the task is hovered or being resized, so it never competes with the
	   words above it. */
	.tevent.task .resize {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
		height: 7px;
		cursor: ns-resize;
		opacity: 0;
		touch-action: none;
	}
	.tevent.task:hover .resize,
	.tevent.task.resizing .resize {
		opacity: 1;
		background: linear-gradient(to bottom, transparent, var(--line-strong));
	}
	.tevent.task.resizing {
		cursor: ns-resize;
		z-index: 5;
	}

	.tevent.task[data-priority='1'] {
		border-left-color: var(--p1);
	}
	.tevent.task[data-priority='2'] {
		border-left-color: var(--p2);
	}
	.tevent.task[data-priority='3'] {
		border-left-color: var(--p3);
	}

	.tevent .at {
		color: var(--ink-faint);
		margin-right: 0.3em;
	}

	.day {
		background: var(--ground);
		min-height: 92px;
		padding: var(--s2);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	/* Seven cells instead of forty-two, so the height a month has to spend on six
	   rows goes into one. It is what makes the week a view rather than only a wider
	   drop target. */
	.calendar.week .day {
		min-height: 320px;
	}

	/* Only shown when the grid has stacked; the strip above says it otherwise. */
	.weekday {
		display: none;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	/* Days from the neighbouring months stay visible but recede — removing them
	   would leave ragged holes at the corners of the grid. */
	.day.outside {
		background: var(--surface-sunken);
	}

	.day.outside .number {
		color: var(--ink-faint);
		opacity: 0.5;
	}

	.number {
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	/* The whole cell, because the whole cell is the target: a day is a box, not a
	   gap between two boxes. */
	.day.over {
		background: var(--surface-raised);
		box-shadow: inset 0 0 0 1px var(--accent);
	}

	.day.today .number {
		color: var(--accent-ink);
		background: var(--accent);
		border-radius: var(--radius-full);
		width: 18px;
		height: 18px;
		display: grid;
		place-items: center;
		font-weight: 560;
	}

	.chip {
		font-size: var(--text-xs);
		text-align: left;
		padding: 1px var(--s1);
		border-radius: var(--radius-sm);
		background: var(--surface);
		box-shadow: inset 0 0 0 1px var(--line);
		border-left: 2px solid var(--line-strong);
		color: var(--ink);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.chip[data-priority='1'] {
		border-left-color: var(--p1);
	}
	.chip[data-priority='2'] {
		border-left-color: var(--p2);
	}
	.chip[data-priority='3'] {
		border-left-color: var(--p3);
	}

	.chip:hover {
		background: var(--surface-sunken);
	}

	/* An event reads as a different kind of thing at a glance, before any of the
	   words are read: a solid bar of colour where a task is a card with a coloured
	   edge. That is the whole distinction the eye needs — a task is something you
	   can move and finish, an event is something that is simply happening.

	   The colour is Google's own for that calendar, so two calendars in one grid
	   are told apart by the colour the person already knows them by. `--line-strong`
	   is the fallback for a calendar Google gave no colour, which is rare and not
	   worth a second decision. */
	.event {
		--event-colour: var(--line-strong);
		font-size: var(--text-xs);
		text-align: left;
		display: block;
		padding: 1px var(--s1);
		border-radius: var(--radius-sm);
		border-left: 3px solid var(--event-colour);
		background: color-mix(in oklab, var(--event-colour) 16%, var(--surface));
		color: var(--ink);
		text-decoration: none;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		/* A drag that cannot land is worse than one that cannot start. The grab
		   cursor is the promise; this withholds it. */
		cursor: default;
	}

	.event[href] {
		cursor: pointer;
	}

	.event:hover {
		background: color-mix(in oklab, var(--event-colour) 26%, var(--surface));
		color: var(--ink);
	}

	/* An all-day event has no clock to show, so the colour carries it instead:
	   filled rather than merely edged, which is what an all-day band looks like in
	   every calendar anybody has used. */
	.event.allday {
		border-left: none;
		background: color-mix(in oklab, var(--event-colour) 22%, var(--surface));
		box-shadow: inset 0 0 0 1px color-mix(in oklab, var(--event-colour) 45%, transparent);
	}

	.at {
		font-variant-numeric: tabular-nums;
		color: var(--ink-faint);
		margin-right: 2px;
	}

	.more {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding-left: var(--s1);
	}

	@media (max-width: 620px) {
		.day {
			min-height: 56px;
			padding: var(--s1);
		}

		.chip {
			font-size: 0;
			padding: 0;
			height: 4px;
			border-radius: var(--radius-full);
			border-left: none;
			background: var(--line-strong);
		}

		.chip[data-priority='1'] {
			background: var(--p1);
		}
		.chip[data-priority='2'] {
			background: var(--p2);
		}
		.chip[data-priority='3'] {
			background: var(--p3);
		}

		/* Counted rather than read, like the task chips beside it — a 45px column
		   fits no title. A bar rather than a dot, so an event is still tellable from
		   a task at a glance: what the month grid says on a phone is "how much is
		   on this day, and how much of it is somebody else's". */
		.event {
			font-size: 0;
			padding: 0;
			height: 4px;
			border-radius: 1px;
			border-left: none;
			box-shadow: none;
			background: var(--event-colour);
		}

		.event.allday {
			background: var(--event-colour);
			box-shadow: none;
		}

		.at {
			display: none;
		}

		.more {
			font-size: 10px;
		}

		/* A week on a phone is not seven columns of 45px. It becomes seven rows —
		   the same information in the shape the screen has room for, and each row
		   is still a drop target, which is the whole point of the view.

		   The chips stay readable here, unlike in the month grid above: a
		   full-width row has room for a title, and a week somebody opened on
		   purpose is one they want to read rather than count. */
		.calendar.week .grid {
			grid-template-columns: 1fr;
		}

		.calendar.week .day {
			min-height: 0;
			flex-direction: row;
			flex-wrap: wrap;
			align-items: baseline;
			gap: var(--s2);
			padding: var(--s2);
		}

		.calendar.week .weekdays {
			display: none;
		}

		.calendar.week .weekday {
			display: inline;
		}

		.calendar.week .chip {
			font-size: var(--text-xs);
			height: auto;
			padding: 1px var(--s1);
			border-radius: var(--radius-sm);
			border-left: 2px solid var(--line-strong);
			background: var(--surface);
			box-shadow: inset 0 0 0 1px var(--line);
		}

		.calendar.week .chip[data-priority='1'] {
			border-left-color: var(--p1);
		}
		.calendar.week .chip[data-priority='2'] {
			border-left-color: var(--p2);
		}
		.calendar.week .chip[data-priority='3'] {
			border-left-color: var(--p3);
		}

		/* Readable again in the week, for the reason the task chips are: a
		   full-width row has room for a title, and a week somebody opened on
		   purpose is one they want to read rather than count. */
		.calendar.week .event {
			font-size: var(--text-xs);
			height: auto;
			padding: 1px var(--s1);
			border-radius: var(--radius-sm);
			border-left: 3px solid var(--event-colour);
			background: color-mix(in oklab, var(--event-colour) 16%, var(--surface));
		}

		.calendar.week .event.allday {
			border-left: none;
			background: color-mix(in oklab, var(--event-colour) 22%, var(--surface));
		}

		.calendar.week .at {
			display: inline;
		}

		.calendar.week .more {
			font-size: var(--text-xs);
		}
	}
</style>
