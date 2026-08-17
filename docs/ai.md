# AI features

Optional, off by default, and bring your own key. verdande works completely without
them — this is a convenience on top of a to-do app, not something the app depends
on.

## Setting up a provider

**Settings → AI**. Four kinds:

=== "Anthropic"

    - **Provider**: Anthropic
    - **Model**: `claude-sonnet-5`
    - **API key**: from [console.anthropic.com](https://console.anthropic.com)

=== "OpenAI"

    - **Provider**: OpenAI
    - **Model**: `gpt-4o`
    - **API key**: from platform.openai.com

=== "Google"

    - **Provider**: Google
    - **Model**: `gemini-2.0-flash`
    - **API key**: from Google AI Studio

=== "Your own model"

    Anything speaking the OpenAI-compatible shape — Ollama, vLLM, LM Studio, a
    company gateway.

    - **Provider**: Compatible
    - **Base URL**: `http://localhost:11434/v1`
    - **Model**: `llama3.1` — whatever you have pulled
    - **API key**: usually blank

    This is why the abstraction exists at all: an integration that only spoke to a
    hosted API would be no use to somebody running their own models.

The key is stored on your account and is **never sent back** to the settings page —
a page that repopulates a password field is one that will eventually leak it into a
screenshot.

## What it can do

**Split a task into sub-tasks.** On any task: *Split with AI*. It suggests two to
seven concrete actions and creates them as sub-tasks. Written in your language.

**A weekly summary.** A short note over what is outstanding: what looks urgent and
what appears to be slipping. At most six lines — it is a prompt to think, not a
report.

## What is sent

Only what the feature needs: for a split, the task's title and notes; for a
summary, the titles, dates and priorities of your open tasks. Never comments,
attachments, or anything from a project the feature was not invoked on.

Nothing is sent to anybody until you configure a provider. With a local model,
nothing leaves your machine at all.

## Turning it off

Clear the model field. Every AI feature reports itself as unavailable rather than
failing, and the rest of the app is unchanged.
