<script>
	/**
	 * Klokken.
	 *
	 * De ting, andre har gjort, som man skal vide: en note, der er delt med en, er
	 * blevet rettet, en opgave er blevet ens, nogen har kommenteret. Ikke ens egne
	 * handlinger — serveren lader være med at fortælle nogen, hvad de lige selv
	 * gjorde — og ikke alt, der sker; en klokke, der ringer ved hver ændring i et
	 * delt projekt, er en klokke, folk holder op med at se på.
	 *
	 * Sætningen skrives her og ikke på serveren. Beskeden bærer *hvad der skete*
	 * (`kind`) og *hvem* (`actor_name`); ordene kommer fra ordbogen, så en klokke i
	 * en engelsk flade er engelsk. Serverens egen `title` er skrevet på dansk til
	 * Web Push, som ikke har nogen ordbog at slå op i, og bruges kun dér.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';
	import { goto } from '$app/navigation';

	let open = $state(false);
	let wrap = $state(null);

	const KINDS = {
		assigned: 'notif.assigned',
		'note.changed': 'notif.noteChanged',
		'note.shared': 'notif.noteShared',
		comment: 'notif.comment'
	};

	// Ukendte slags falder tilbage på serverens egen sætning frem for at forsvinde:
	// en besked fra en nyere serverbygning end denne flade skal stadig kunne læses.
	const headline = (n) => (KINDS[n.kind] ? t(KINDS[n.kind], { name: n.actor_name ?? '' }) : n.title);

	function when(iso) {
		const minutes = Math.round((Date.now() - new Date(iso)) / 60000);
		if (minutes < 1) return t('notif.justNow');
		if (minutes < 60) return t('notif.minutes', { n: String(minutes) });
		if (minutes < 60 * 24) return t('notif.hours', { n: String(Math.round(minutes / 60)) });
		return new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
	}

	/**
	 * Hvor en besked fører hen.
	 *
	 * En note åbnes på notesiden med sit id i adressen; en opgave åbnes i sin egen
	 * rude oven på det, man stod i. Det er samme valg som alle andre steder i
	 * fladen: en opgave er noget, man kigger på og lukker igen, en note er et sted,
	 * man går hen.
	 */
	function follow(n) {
		open = false;
		if (!n.read) app.markRead(n.id);
		if (n.note_id) goto(`/noter?note=${n.note_id}`);
		else if (n.task_id) app.openDetail(n.task_id);
		else if (n.project_id) goto(`/projekt/${n.project_id}`);
	}

	function outside(event) {
		if (open && wrap && !wrap.contains(event.target)) open = false;
	}
</script>

<svelte:window
	onclick={outside}
	onkeydown={(e) => {
		if (e.key === 'Escape' && open) open = false;
	}}
/>

<div class="wrap" bind:this={wrap}>
	<button
		class="bell"
		class:on={open}
		onclick={() => (open = !open)}
		aria-expanded={open}
		aria-haspopup="dialog"
		aria-label={app.unread
			? t('notif.titleWithCount', { n: String(app.unread) })
			: t('notif.title')}
		title={t('notif.title')}
	>
		<svg viewBox="0 0 24 24" aria-hidden="true">
			<path d="M18 8a6 6 0 10-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9" />
			<path d="M10.3 21a2 2 0 003.4 0" />
		</svg>
		{#if app.unread}
			<!-- Et tal, ikke en prik. "Der er sket noget" er en anden besked end "der
			     er sket fjorten ting", og den anden er den, der får folk til at kigge. -->
			<span class="count" aria-hidden="true">{app.unread > 9 ? '9+' : app.unread}</span>
		{/if}
	</button>

	{#if open}
		<div class="panel" role="dialog" aria-label={t('notif.title')}>
			<header>
				<span>{t('notif.title')}</span>
				{#if app.unread}
					<button class="allread" onclick={() => app.markRead()}>{t('notif.markAllRead')}</button>
				{/if}
			</header>

			{#if app.notifications.length}
				<ul>
					{#each app.notifications as n (n.id)}
						<li>
							<button class="row" class:unread={!n.read} onclick={() => follow(n)}>
								<span class="what">{headline(n)}</span>
								{#if n.body}<span class="about">{n.body}</span>{/if}
								<span class="ago">{when(n.created_at)}</span>
							</button>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="none">{t('notif.empty')}</p>
			{/if}
		</div>
	{/if}
</div>

<style>
	.wrap {
		position: relative;
	}

	.bell {
		position: relative;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		border: none;
		background: none;
		border-radius: var(--radius);
		color: var(--ink-muted);
		cursor: pointer;
	}
	.bell:hover,
	.bell.on {
		background: var(--surface-raised);
		color: var(--ink);
	}
	.bell svg {
		width: 18px;
		height: 18px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.7;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.count {
		position: absolute;
		top: 1px;
		right: 0;
		min-width: 15px;
		padding: 0 3px;
		border-radius: var(--radius-full);
		background: var(--accent);
		color: var(--on-accent, #fff);
		font-size: 10px;
		line-height: 15px;
		font-weight: 600;
		text-align: center;
	}

	.panel {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		z-index: 40;
		width: min(340px, calc(100vw - var(--s3) * 2));
		max-height: 60vh;
		overflow-y: auto;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		box-shadow: var(--shadow-lg, 0 8px 30px rgb(0 0 0 / 0.25));
	}

	.panel header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--s2);
		padding: var(--s2);
		border-bottom: 1px solid var(--line);
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	.allread {
		border: none;
		background: none;
		padding: 0;
		font: inherit;
		color: var(--accent);
		cursor: pointer;
	}
	.allread:hover {
		text-decoration: underline;
	}

	.panel ul {
		list-style: none;
		margin: 0;
		padding: 0;
	}

	.row {
		display: grid;
		gap: 2px;
		width: 100%;
		padding: var(--s2);
		border: none;
		border-bottom: 1px solid var(--line);
		background: none;
		text-align: left;
		cursor: pointer;
		color: var(--ink-muted);
	}
	.row:hover {
		background: var(--surface-raised);
	}

	/* Ulæst er en vægt og en prik, ikke en farvet baggrund. Baggrunden var det
	   første forsøg, og den gjorde listen til et bånd af firkanter, hvor man ikke
	   kunne se hvad der var én besked. */
	.row.unread .what {
		font-weight: 600;
		color: var(--ink);
	}
	.row.unread .what::before {
		content: '';
		display: inline-block;
		width: 6px;
		height: 6px;
		margin-right: 6px;
		vertical-align: middle;
		border-radius: var(--radius-full);
		background: var(--accent);
	}

	.what {
		font-size: var(--text-sm);
	}

	.about {
		font-size: var(--text-xs);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.ago {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.none {
		margin: 0;
		padding: var(--s3) var(--s2);
		text-align: center;
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}
</style>
