<script>
	/** I dag: what is overdue, and what is due today. */
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import QuickAdd from '$lib/components/QuickAdd.svelte';
	import { t, tag } from '$lib/i18n.svelte.js';
	import { humanMessage } from '$lib/api.js';

	let loaded = $state(false);

	// The local date, not the server's: "today" is a question about where the
	// person is standing.
	const isoToday = (() => {
		const now = new Date();
		return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(
			now.getDate()
		).padStart(2, '0')}`;
	})();


	/**
	 * Dagens plan, dér hvor man kigger, når man starter dagen.
	 *
	 * Hentet og ikke regnet forfra: det er den samme tekst, beskeden i morges bar.
	 * En plan, der siger noget andet, når man kigger på den, end den sagde, da den
	 * kom, er værre end begge dele hver for sig — og en model pr. sidevisning er
	 * dyr for noget, der ikke har ændret sig.
	 *
	 * Lukket for i dag frem for slettet. Kortet er en påmindelse, ikke en opgave,
	 * og den, der har læst den, skal kunne få den af vejen uden at miste den —
	 * derfor står lukningen i localStorage med dagens dato og ikke på kontoen.
	 */
	let plan = $state(null);
	let planBusy = $state(false);
	let planError = $state('');
	const DISMISS_KEY = 'verdande:plan-dismissed';

	let dismissed = $state(
		typeof localStorage !== 'undefined' && localStorage.getItem(DISMISS_KEY) === isoToday
	);

	function dismissPlan() {
		dismissed = true;
		try {
			localStorage.setItem(DISMISS_KEY, isoToday);
		} catch {
			// Privat vindue; kortet er væk til næste indlæsning.
		}
	}

	async function makePlan() {
		if (planBusy) return;
		planBusy = true;
		planError = '';
		try {
			// Stille: man står og kigger på den. En besked om noget, man har foran
			// sig, er den slags, der lærer folk at ignorere klokken.
			const made = await api.planNow({ silent: true });
			plan = { ...plan, plan: made.plan, plan_at: made.plan_at };
			dismissed = false;
		} catch (e) {
			planError = humanMessage(e);
		} finally {
			planBusy = false;
		}
	}

	$effect(() => {
		api
			.planSettings()
			.then((p) => (plan = p))
			.catch(() => {
				// Et kort, der ikke kunne hentes, må ikke stå i vejen for listen.
			});
	});

	const planMade = $derived(
		plan?.plan_at
			? new Date(plan.plan_at * 1000).toLocaleTimeString(tag(), {
					hour: '2-digit',
					minute: '2-digit'
				})
			: ''
	);

	async function load() {
		const data = await api.today();
		app.tasks = [...data.overdue, ...data.today];
		loaded = true;
	}

	$effect(() => {
		load();
	});

	// Derived from the store by *date*, not from the ids that came back with the
	// first request. Filtering against a captured list would mean a task added
	// through quick add — which is pushed into the store, not into that list —
	// never appeared until a reload, which is exactly the moment the interface has
	// to feel immediate.
	let liveOverdue = $derived(
		app.tasks.filter((t) => !t.completed && t.due_date && t.due_date < isoToday)
	);
	let liveToday = $derived(app.tasks.filter((t) => !t.completed && t.due_date === isoToday));

	let date = $derived(
		new Date().toLocaleDateString(tag(), {
			weekday: 'long',
			day: 'numeric',
			month: 'long'
		})
	);
</script>

<div class="view">
	<header>
		<h1>{t('nav.today')}</h1>
		<p>{date}</p>
	</header>

	<QuickAdd />

	{#if plan?.plan && !dismissed}
		<!-- Teksten som den blev sendt, og klokkeslættet den blev lavet: en plan fra
		     klokken syv er en anden slags oplysning klokken fire end klokken otte. -->
		<section class="plan">
			<header>
				<h2>{t('ai.plan')}</h2>
				{#if planMade}<span class="made">{t('ai.planMade', { time: planMade })}</span>{/if}
				<button class="close" onclick={dismissPlan} aria-label={t('ai.planDismiss')}>×</button>
			</header>
			<p>{plan.plan}</p>
			<button class="again" onclick={makePlan} disabled={planBusy}>
				{planBusy ? t('ai.thinking') : t('ai.planAgain')}
			</button>
		</section>
	{:else if plan && !plan.plan && (liveToday.length || liveOverdue.length)}
		<!-- Ingen plan i dag: enten er beskeden slået fra, eller også er timen ikke
		     kommet endnu. Knappen frem for ingenting, så den, der har slået
		     morgenbeskeden fra, stadig kan bede om planen. Kun når der er noget at
		     lægge en plan om — en tom dag har ikke brug for en knap. -->
		<p class="planask">
			<button class="again" onclick={makePlan} disabled={planBusy}>
				{planBusy ? t('ai.thinking') : t('ai.planMake')}
			</button>
			{#if planError}<span class="planerror">{planError}</span>{/if}
		</p>
	{/if}

	{#if liveOverdue.length}
		<section>
			<h2 class="overdue">{t('view.overdue')}</h2>
			{#each liveOverdue as task (task.id)}
				<TaskRow {task} />
			{/each}
		</section>
	{/if}

	<section>
		{#if liveOverdue.length}
			<h2>{t('nav.today')}</h2>
		{/if}
		{#each liveToday as task (task.id)}
			<TaskRow {task} />
		{/each}

		{#if loaded && !liveToday.length && !liveOverdue.length}
			<!-- An empty Today is the goal, not a failure state. It should feel
			     like finishing, not like something is missing. -->
			<p class="clear">
				<span class="rune" aria-hidden="true">ᚹ</span>
				{t('view.nothingMoreToday')}
			</p>
		{/if}
	</section>
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	header {
		margin-bottom: var(--s5);
	}

	/* Kortet ligger mellem feltet og listen: under det, man skriver i, og over det,
	   man skal se på. Dæmpet frem for fremhævet — det er en påmindelse om, hvad
	   dagen indeholder, ikke en opgave, der skal laves. */
	.plan {
		margin: 0 0 var(--s5);
		padding: var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		background: var(--surface-raised, var(--surface));
	}

	.plan header {
		display: flex;
		align-items: baseline;
		gap: var(--s2);
		margin: 0 0 var(--s2);
	}

	.plan h2 {
		font-size: var(--text-sm);
		margin: 0;
	}

	.plan .made {
		color: var(--ink-faint);
		font-size: var(--text-xs);
	}

	.plan .close {
		margin-left: auto;
		border: none;
		background: none;
		color: var(--ink-faint);
		cursor: pointer;
		line-height: 1;
		font-size: var(--text-md);
	}

	/* Linjeskift bevaret: planen er to-tre linjer, og de er skrevet som linjer. */
	.plan p {
		margin: 0 0 var(--s2);
		font-size: var(--text-sm);
		line-height: 1.6;
		white-space: pre-line;
	}

	.again {
		border: none;
		background: none;
		padding: 0;
		font: inherit;
		font-size: var(--text-xs);
		color: var(--accent);
		cursor: pointer;
	}

	.again:disabled {
		color: var(--ink-faint);
		cursor: default;
	}

	.planask {
		margin: 0 0 var(--s5);
	}

	.planerror {
		margin-left: var(--s2);
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	h1 {
		font-size: var(--text-2xl);
	}

	header p {
		margin: var(--s1) 0 0;
		color: var(--ink-faint);
		font-size: var(--text-sm);
		text-transform: capitalize;
	}

	section {
		margin-top: var(--s5);
	}

	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
		padding: 0 var(--s2) var(--s2);
	}

	h2.overdue {
		color: var(--p1);
	}

	.clear {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--s3);
		padding: var(--s8) var(--s4);
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}

	.rune {
		font-size: var(--text-2xl);
		color: var(--accent);
		opacity: 0.5;
	}
</style>
