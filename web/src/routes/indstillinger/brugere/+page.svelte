<script>
	/**
	 * Brugere: who is on this instance, and how somebody else gets on it.
	 *
	 * There is no open registration and no account created with a password
	 * somebody else chose. Adding a person means issuing an invite with no project
	 * attached; the link ends with a password only its owner has seen. So this page
	 * has two lists — the accounts, and the invites that have gone out and not been
	 * used yet.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';
	import { ago, shortDate } from '$lib/when.js';

	let users = $state([]);
	let invites = $state([]);
	let email = $state('');
	let link = $state('');
	let errors = $state({});
	let busy = $state(false);

	$effect(() => {
		load();
	});

	async function load() {
		try {
			const r = await api.listUsers();
			users = r.users;
			invites = r.invites;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function invite(event) {
		event.preventDefault();
		if (busy) return;
		busy = true;
		errors = {};
		link = '';
		try {
			const result = await api.inviteUser(email);
			email = '';
			// Shown whether or not it was emailed. With no mail server this is the
			// only way it reaches anybody, and with one it saves waiting on
			// delivery to find out whether it worked.
			link = result.link;
			await load();
		} catch (e) {
			errors = e.fields ?? {};
			if (!Object.keys(errors).length) app.toast(humanMessage(e));
		} finally {
			busy = false;
		}
	}

	async function setAdmin(user, isAdmin) {
		const previous = users;
		users = users.map((u) => (u.id === user.id ? { ...u, is_admin: isAdmin } : u));
		try {
			await api.setUserAdmin(user.id, isAdmin);
		} catch (e) {
			users = previous;
			app.toast(humanMessage(e));
		}
	}

	/**
	 * Deleting an account is the only thing in this app that cannot be undone.
	 *
	 * Everything else is soft-deleted with a trash behind it. This is not: the
	 * person's own projects go, and every task in them. What they wrote in somebody
	 * else's project stays and loses its author — `tasks.created_by` is ON DELETE
	 * SET NULL since migration 0008. Both numbers are said out loud, because they
	 * are two different sentences and the second one is a reassurance rather than a
	 * warning.
	 */
	async function remove(user) {
		const parts = [t('users.deleteQuestion', { name: user.name, email: user.email }), ''];
		if (user.project_count || user.task_count) {
			parts.push(
				t('users.deleteTakes', {
					projects: count(user.project_count, 'users.projectOne', 'users.projectMany'),
					tasks: count(user.task_count, 'users.taskOne', 'users.taskMany')
				}),
				''
			);
		}
		if (user.authored_elsewhere) {
			parts.push(
				t('users.deleteLeaves', {
					tasks: count(user.authored_elsewhere, 'users.taskOne', 'users.taskMany')
				}),
				''
			);
		}
		parts.push(t('users.deleteFinal'));
		if (!confirm(parts.join('\n'))) return;

		const previous = users;
		users = users.filter((u) => u.id !== user.id);
		try {
			await api.deleteUser(user.id);
		} catch (e) {
			users = previous;
			app.toast(humanMessage(e));
		}
	}

	async function revoke(pending) {
		if (!confirm(t('users.revokeQuestion', { email: pending.email }))) return;

		const previous = invites;
		invites = invites.filter((i) => i.id !== pending.id);
		try {
			await api.revokeInvite(pending.id);
		} catch (e) {
			invites = previous;
			app.toast(humanMessage(e));
		}
	}

	const count = (n, one, many) => `${n} ${t(n === 1 ? one : many)}`;


	const expires = shortDate;
</script>

<section class="panel">
	<header>
		<h2>{t('users.invite')}</h2>
		<p class="hint">
			{t('users.inviteHint')}
		</p>
	</header>

	<form onsubmit={invite}>
		<div class="field">
			<label for="ny-email">{t('users.emailAddress')}</label>
			<input
				id="ny-email"
				type="email"
				bind:value={email}
				placeholder={t('users.emailPlaceholder')}
				required
				aria-invalid={errors.email ? 'true' : undefined}
			/>
			{#if errors.email}<p class="error">{t('users.mustBeEmail')}</p>{/if}
		</div>
		<div class="row">
			<button class="primary" type="submit" disabled={busy}>{t('users.sendInvite')}</button>
		</div>
	</form>

	{#if link}
		<div class="field">
			<p class="hint">{t('users.inviteMade')}</p>
			<code class="link-out">{link}</code>
		</div>
	{/if}
</section>

{#if invites.length}
	<section class="panel">
		<header>
			<h2>{t('users.pending')}</h2>
			<p class="hint">
				{t('users.pendingHint')}
			</p>
		</header>

		<ul class="rows">
			{#each invites as pending (pending.id)}
				<li>
					<div class="what">
						<span class="primary-line">{pending.email}</span>
						<span class="secondary">
							{pending.project_name ? `til ${pending.project_name}` : 'til instansen'}
							· {t('tokens.expiresOn', { when: expires(pending.expires_at) })}
							{#if pending.invited_by}· inviteret af {pending.invited_by}{/if}
						</span>
					</div>
					<button class="secondary" onclick={() => revoke(pending)}>{t('users.revoke')}</button>
				</li>
			{/each}
		</ul>
	</section>
{/if}

<section class="panel">
	<header>
		<h2>{t('users.accounts')}</h2>
		<p class="hint">{t('users.accountsHint')}</p>
	</header>

	<ul class="rows">
		{#each users as user (user.id)}
			<li>
				<span class="avatar" style="background: {user.avatar_color}">
					{user.name[0]?.toUpperCase() ?? '?'}
				</span>
				<div class="what">
					<span class="primary-line">
						{user.name}
						{#if user.is_admin}<span class="badge">{t('users.admin')}</span>{/if}
						{#if user.self}<span class="badge quiet">{t('users.you')}</span>{/if}
					</span>
					<span class="secondary">
						{user.email} · {ago(user.last_seen_at, { never: 'when.neverSignedIn' })}
						{#if user.project_count}· {count(user.project_count, 'projekt', 'projekter')}{/if}
					</span>
				</div>

				<!-- Nobody demotes or deletes themselves from here. The server refuses
				     both, and a button that exists only to be refused is a button that
				     wastes a click to teach you a rule. -->
				{#if !user.self}
					<button class="secondary" onclick={() => setAdmin(user, !user.is_admin)}>
						{user.is_admin ? t('users.removeAdmin') : t('users.makeAdmin')}
					</button>
					<button class="danger" onclick={() => remove(user)}>{t('users.delete')}</button>
				{/if}
			</li>
		{/each}
	</ul>
</section>

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
	}

	.rows li {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s3) 0;
		border-bottom: 1px solid var(--line);
		flex-wrap: wrap;
	}

	.rows li:last-child {
		border-bottom: 0;
	}

	.avatar {
		width: 28px;
		height: 28px;
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		font-size: var(--text-xs);
		font-weight: 560;
		color: #fff;
		flex: none;
	}

	.what {
		flex: 1;
		min-width: 160px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.primary-line {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
		flex-wrap: wrap;
	}

	.secondary {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.badge {
		font-size: var(--text-xs);
		color: var(--accent);
		border: 1px solid var(--accent);
		border-radius: var(--radius-full);
		padding: 0 var(--s2);
	}

	.badge.quiet {
		color: var(--ink-faint);
		border-color: var(--line-strong);
	}

	.link-out {
		display: block;
		margin-top: var(--s1);
		padding: var(--s2);
		background: var(--surface-sunken);
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		overflow-wrap: anywhere;
		color: var(--ink);
	}
</style>
