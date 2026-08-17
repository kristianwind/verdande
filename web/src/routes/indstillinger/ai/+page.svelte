<script>
	/** The AI provider: which one, which model, and the key. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';

	let settings = $state(null);
	let apiKey = $state('');
	let saving = $state(false);
	let saved = $state(false);
	let errors = $state({});

	let summary = $state('');
	let summarising = $state(false);

	const PROVIDERS = [
		{ value: '', label: 'Slået fra' },
		{ value: 'anthropic', label: 'Anthropic' },
		{ value: 'openai', label: 'OpenAI' },
		{ value: 'google', label: 'Google' },
		{ value: 'compatible', label: 'OpenAI-kompatibel (Ollama, LM Studio, …)' }
	];

	// Suggestions, not a closed list: the model field is free text, because a new
	// model name must not require a new release of this app to be usable.
	const SUGGESTED_MODEL = {
		anthropic: 'claude-sonnet-5',
		openai: 'gpt-5',
		google: 'gemini-2.5-pro',
		compatible: 'llama3.1'
	};

	$effect(() => {
		api
			.getAISettings()
			.then((s) => (settings = s))
			.catch((e) => app.toast(humanMessage(e)));
	});

	async function save(event) {
		event.preventDefault();
		saving = true;
		saved = false;
		errors = {};
		try {
			await api.setAISettings({
				provider: settings.provider,
				base_url: settings.base_url ?? '',
				model: settings.model ?? '',
				// An empty key means "leave the stored one alone" on the server. That
				// is why this field starts empty rather than pre-filled with dots.
				api_key: apiKey
			});
			apiKey = '';
			settings = await api.getAISettings();
			saved = true;
		} catch (e) {
			errors = e.fields ?? {};
			if (!Object.keys(errors).length) app.toast(humanMessage(e));
		} finally {
			saving = false;
		}
	}

	async function weeklySummary() {
		summarising = true;
		summary = '';
		try {
			summary = (await api.aiSummary()).summary;
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			summarising = false;
		}
	}
</script>

<section class="panel">
	<header>
		<h2>AI</h2>
		<p class="hint">
			Bruges to steder: at dele en opgave op i undertasks, og at skrive et kort
			overblik over det, der er udestående. Nøglen ligger på din server og sendes
			kun til den udbyder, du vælger her.
		</p>
	</header>

	{#if settings === null}
		<p class="empty">…</p>
	{:else}
		<form onsubmit={save}>
			<div class="field">
				<label for="provider">Udbyder</label>
				<select id="provider" bind:value={settings.provider}>
					{#each PROVIDERS as provider (provider.value)}
						<option value={provider.value}>{provider.label}</option>
					{/each}
				</select>
				{#if errors.provider}<p class="error">{errors.provider}</p>{/if}
			</div>

			{#if settings.provider}
				<div class="field">
					<label for="model">Model</label>
					<input
						id="model"
						bind:value={settings.model}
						placeholder={SUGGESTED_MODEL[settings.provider] ?? ''}
					/>
				</div>

				{#if settings.provider === 'compatible'}
					<div class="field">
						<label for="base">Adresse</label>
						<input
							id="base"
							class="mono"
							bind:value={settings.base_url}
							placeholder="http://localhost:11434/v1"
						/>
					</div>
				{/if}

				<div class="field">
					<label for="key">API-nøgle</label>
					<input
						id="key"
						type="password"
						autocomplete="off"
						bind:value={apiKey}
						placeholder={settings.has_key ? 'Der er gemt en nøgle — lad feltet stå tomt' : ''}
					/>
					<p class="hint">
						{#if settings.has_key}
							Feltet er tomt med vilje. Nøglen sendes aldrig tilbage til browseren
							— en indstillingsside, der fylder et kodeordsfelt ud igen, er en side,
							der en dag lækker en nøgle ud i et skærmbillede.
						{:else}
							Gemmes på serveren og vises aldrig igen.
						{/if}
					</p>
				</div>
			{/if}

			<div class="row">
				<button class="primary" type="submit" disabled={saving}>Gem</button>
				{#if saved}<span class="saved">Gemt.</span>{/if}
			</div>
		</form>
	{/if}
</section>

{#if settings?.provider && settings?.has_key}
	<section class="panel">
		<header>
			<h2>Ugentligt overblik</h2>
			<p class="hint">En kort note om, hvad der ser vigtigst ud lige nu.</p>
		</header>

		<div class="row">
			<button class="secondary" onclick={weeklySummary} disabled={summarising}>
				{summarising ? 'Skriver …' : 'Skriv et overblik'}
			</button>
		</div>

		{#if summary}
			<p class="summary">{summary}</p>
		{/if}
	</section>
{/if}

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.summary {
		margin: 0;
		padding: var(--s4);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		line-height: 1.6;
		white-space: pre-wrap;
	}
</style>
