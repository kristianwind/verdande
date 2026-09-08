// Package ai is a provider-agnostic layer over chat completions.
//
// One interface, adapters for Anthropic, OpenAI, Google, and anything speaking the
// OpenAI-compatible shape — which is what a locally-run model behind Ollama, vLLM
// or LM Studio offers. The last of those is why the abstraction exists at all: the
// owner runs their own models, and an integration that only spoke to a hosted API
// would be useless to them.
//
// Every feature that uses this degrades to nothing when no provider is configured.
// AI here is a convenience on top of a to-do app that works without it, not a
// dependency the app has acquired.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/kristianwind/verdande/internal/safedial"
	"strings"
	"time"
)

// ErrNotConfigured is returned when no provider is set up. Callers treat it as
// "this feature is off", not as a failure.
var ErrNotConfigured = errors.New("ai: no provider is configured")

type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderGoogle    Provider = "google"
	// ProviderCompatible is any OpenAI-shaped endpoint: Ollama, vLLM, LM Studio,
	// OpenRouter, a company gateway. Base URL and model are configured by hand.
	ProviderCompatible Provider = "compatible"
)

type Config struct {
	Provider Provider
	APIKey   string
	// BaseURL overrides the provider's default endpoint. Required for
	// ProviderCompatible and useful for a proxy in front of the others.
	BaseURL string
	Model   string
	// AllowPrivate lets BaseURL be on this machine or its private network.
	//
	// Set by the caller from who is asking, never read from storage: it is the
	// answer to "may this person reach the LAN", and that answer can change after
	// the address was saved. See the handler for who gets it and why.
	AllowPrivate bool
}

func (c Config) Configured() bool {
	if c.Provider == "" || c.Model == "" {
		return false
	}
	// A local model needs no key, which is exactly the case a key check would
	// break — so the requirement is per-provider rather than universal.
	if c.Provider == ProviderCompatible {
		return c.BaseURL != ""
	}
	return c.APIKey != ""
}

type Message struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// Client talks to whichever provider is configured.
type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	dial := safedial.Client
	if cfg.AllowPrivate {
		dial = safedial.AllowPrivate
	}
	return &Client{
		cfg: cfg,
		// Generous: a local model on modest hardware can take a while to produce
		// its first token, and the alternative is a timeout that only ever fires
		// for the people running their own.
		// safedial by default. `base_url` is typed into a field in the interface,
		// so it is a user-supplied address — and a server that fetches a
		// user-supplied address is a way to ask it to reach things the caller
		// cannot: the panel next door, a database on the same bridge, the cloud
		// metadata endpoint. On a homelab that is most of what is worth reaching.
		//
		// The check is on the resolved address rather than on the URL, because a
		// name answers whatever its owner says — and can answer differently the
		// second time, after a parse-time check has passed.
		//
		// And then the exception, which is the whole reason this provider exists:
		// a model you run yourself IS on that network. Blocking it made the two
		// lines above true and the feature useless — the comment about a local
		// model being slow to its first token was written for a case the dialler
		// refused to reach. AllowPrivate is granted per request, not per address.
		http: dial(120 * time.Second),
	}
}

// Complete sends a system prompt and a conversation, and returns the reply.
func (c *Client) Complete(ctx context.Context, system string, messages []Message) (string, error) {
	if !c.cfg.Configured() {
		return "", ErrNotConfigured
	}
	switch c.cfg.Provider {
	case ProviderAnthropic:
		return c.anthropic(ctx, system, messages)
	case ProviderGoogle:
		return c.google(ctx, system, messages)
	default:
		// OpenAI and every compatible endpoint share one request shape.
		return c.openAICompatible(ctx, system, messages)
	}
}

// --- Anthropic --------------------------------------------------------------------

func (c *Client) anthropic(ctx context.Context, system string, messages []Message) (string, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}

	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": 2048,
		"system":     system,
		"messages":   messages,
	}
	req, err := c.request(ctx, base+"/v1/messages", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}

	var out strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}

// --- OpenAI and compatible ----------------------------------------------------------

func (c *Client) openAICompatible(ctx context.Context, system string, messages []Message) (string, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = "https://api.openai.com"
	}
	base = strings.TrimSuffix(base, "/")
	// A locally-run endpoint is usually given as ".../v1" already; appending it
	// again is the single most common way this is misconfigured.
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}

	all := append([]map[string]string{{"role": "system", "content": system}}, toOpenAI(messages)...)
	req, err := c.request(ctx, base+"/chat/completions", map[string]any{
		"model":    c.cfg.Model,
		"messages": all,
	})
	if err != nil {
		return "", err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("ai: the model returned nothing")
	}
	return parsed.Choices[0].Message.Content, nil
}

func toOpenAI(messages []Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, map[string]string{"role": m.Role, "content": m.Content})
	}
	return out
}

// --- Google -------------------------------------------------------------------------

func (c *Client) google(ctx context.Context, system string, messages []Message) (string, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}

	contents := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model" // Google's name for the same thing
		}
		contents = append(contents, map[string]any{
			"role": role, "parts": []map[string]string{{"text": m.Content}},
		})
	}

	body := map[string]any{
		"contents":          contents,
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", base, c.cfg.Model, c.cfg.APIKey)
	req, err := c.request(ctx, url, body)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Candidates) == 0 {
		return "", fmt.Errorf("ai: the model returned nothing")
	}

	var out strings.Builder
	for _, part := range parsed.Candidates[0].Content.Parts {
		out.WriteString(part.Text)
	}
	return out.String(), nil
}

// --- plumbing ---------------------------------------------------------------------------

func (c *Client) request(ctx context.Context, url string, body any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		// The provider's own message is included: for a local model it is usually
		// the actual problem ("model not found"), and hiding it would leave the
		// operator with nothing to go on.
		// The provider's body is no longer passed back to the caller.
		//
		// It was, and combined with a free-text base URL that made a working
		// internal scanner: point it at a host, read the first three hundred bytes
		// of whatever answered. The operator still needs the message — for a local
		// model it is usually the actual problem — so it goes to the log, where it
		// is available to the person running the instance and to nobody else.
		return &upstreamError{Host: req.URL.Host, Status: resp.Status, Body: truncate(string(raw), 300)}
	}
	return json.Unmarshal(raw, out)
}

// upstreamError keeps the provider's own words for the log and away from the
// response. Error() is what reaches a caller; Detail() is what is written down.
type upstreamError struct {
	Host   string
	Status string
	Body   string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("ai: %s said %s", e.Host, e.Status)
}

// Detail is the whole of it, for the instance's own error log.
func (e *upstreamError) Detail() string {
	return fmt.Sprintf("ai: %s said %s: %s", e.Host, e.Status, e.Body)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// --- features ------------------------------------------------------------------------

// SplitIntoSubtasks asks for a task to be broken down.
//
// The prompt insists on a bare JSON array because the result is parsed, and a model
// that helpfully wraps it in prose or a code fence produces a parse failure that
// looks to the user like the feature being broken.
func (c *Client) SplitIntoSubtasks(ctx context.Context, task, description, locale string) ([]string, error) {
	language := "Danish"
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		language = "English"
	}

	system := fmt.Sprintf(`You break a task into concrete sub-tasks.

Reply with a JSON array of strings and nothing else — no prose, no code fence.
Write the sub-tasks in %s. Give between two and seven of them. Each one should be
a single concrete action somebody could do in one sitting, phrased the way the
original task is phrased. Do not restate the original task as a sub-task.`, language)

	prompt := "Task: " + task
	if description != "" {
		prompt += "\nNotes: " + description
	}

	reply, err := c.Complete(ctx, system, []Message{{Role: "user", Content: prompt}})
	if err != nil {
		return nil, err
	}

	var subtasks []string
	if err := json.Unmarshal([]byte(extractJSON(reply)), &subtasks); err != nil {
		return nil, fmt.Errorf("ai: the model did not return a list: %s", truncate(reply, 200))
	}

	out := make([]string, 0, len(subtasks))
	for _, s := range subtasks {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ai: the model returned an empty list")
	}
	return out, nil
}

// WeeklySummary produces a short prioritisation note over what is outstanding.
func (c *Client) WeeklySummary(ctx context.Context, tasks []string, locale string) (string, error) {
	language := "Danish"
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		language = "English"
	}

	system := fmt.Sprintf(`You help somebody decide what to do this week.

Write in %s. Be brief — at most six short lines. Say what looks most urgent and
why, and name anything that appears to be slipping. Do not restate the whole list
back, do not congratulate, and do not offer productivity advice.`, language)

	prompt := "These are the open tasks:\n" + strings.Join(tasks, "\n")
	return c.Complete(ctx, system, []Message{{Role: "user", Content: prompt}})
}

// Suggestion er én foreslået opgave, skrevet som en linje, man selv kunne have
// tastet.
//
// `line` er quick-add-syntaks — "Ring til Anders i morgen p1 #Firma" — og ikke et
// felt pr. egenskab. To grunde, og den anden er den vigtige. Parseren findes
// allerede og er den samme, uanset hvor teksten kom fra, så der er ingen anden
// fortolkning at holde i takt med den. Og et forslag, der er skrevet i det sprog,
// man selv skriver i, kan læses og rettes af den, det bliver stillet til — hvor
// et sæt felter, en model har udfyldt, er noget, man skal tage eller lade være.
//
// `why` er én sætning om, hvorfor den ser sådan ud. Den står ved siden af
// forslaget, så et forkert gæt kan gennemskues frem for bare at være forkert.
type Suggestion struct {
	Line string `json:"line"`
	Why  string `json:"why,omitempty"`
}

// TidyInbox foreslår, hvordan løse opgaver i indbakken kunne skrives færdig.
//
// Modellen får de projekter, der findes, og må kun bruge dem: et `#Nyt Projekt`,
// den fandt på, ville lave et projekt ved et uheld, første gang nogen sagde ja.
func (c *Client) TidyInbox(ctx context.Context, tasks, projects []string, today, locale string) ([]Suggestion, error) {
	language := languageOf(locale)

	known := "none"
	if len(projects) > 0 {
		known = "#" + strings.Join(projects, ", #")
	}

	system := fmt.Sprintf(`You tidy up a task inbox.

For each task you are given, write the line the person would have typed if they
had filed it properly. Keep their own words for what the task is — you are filing
it, not rewriting it.

The line may carry, in this syntax and no other:
  #Project   one of these existing projects, and never one that is not: %s
  p1..p4     priority, where p1 is most urgent
  a date in plain %s, such as "i morgen", "on friday", "15/3"

Leave out anything the task does not tell you. A guessed deadline is worse than
none: the person can add one, but they cannot see a wrong one you invented.
Today is %s.

Reply with a JSON array and nothing else — no prose, no code fence. Each element
is {"line": "...", "why": "..."} where why is at most one short sentence in %s
saying what you read out of the task. Give one element per task, in the order you
were given them, and leave a task out entirely if it is already filed well.`,
		known, language, today, language)

	prompt := "Tasks:\n" + strings.Join(tasks, "\n")
	return c.suggestions(ctx, system, prompt)
}

// ActionsInNote trækker det, nogen har lovet, ud af en note.
//
// Beskrivelsen er stram med vilje. En model, der får lov at "finde opgaver" i en
// tekst, finder også emner, overskrifter og gode idéer — og så er resultatet en
// liste, man skal luge i, hvilket er dyrere end at skrive de tre punkter selv.
func (c *Client) ActionsInNote(ctx context.Context, note string, projects []string, today, locale string) ([]Suggestion, error) {
	language := languageOf(locale)

	known := "none"
	if len(projects) > 0 {
		known = "#" + strings.Join(projects, ", #")
	}

	system := fmt.Sprintf(`You read a note and pull out what somebody has to do.

Only things somebody committed to or was asked to do. A heading is not a task, a
topic that was discussed is not a task, and an idea nobody agreed to is not a
task. If the note contains none, reply with an empty array — that is a useful
answer and a made-up list is not.

Write each one as the line the person would have typed, in %s, in this syntax and
no other:
  #Project   one of these existing projects, and never one that is not: %s
  p1..p4     priority, where p1 is most urgent
  a date in plain %s, only when the note actually gives one

Today is %s.

Reply with a JSON array and nothing else — no prose, no code fence. Each element
is {"line": "...", "why": "..."} where why quotes the few words in the note that
the task came from, so it can be checked.`, language, known, language, today)

	return c.suggestions(ctx, system, "Note:\n"+note)
}

// DailyPlan skriver morgenens to-tre linjer.
//
// Tallene er talt i basen og gives med som kendsgerninger, modellen ikke må regne
// om. En plan, der siger fire opgaver, hvor der er tre, er værre end ingen plan —
// og en model, der tæller en liste, tæller den forkert af og til.
//
// Det ene sted, den må mene noget, er hvad man skulle begynde med, og hvad der
// kunne vente. Det er dét, der gør det til en plan frem for en optælling.
func (c *Client) DailyPlan(ctx context.Context, tasks []string, due, overdue int, locale string) (string, error) {
	language := languageOf(locale)

	system := fmt.Sprintf(`You write somebody's morning note about their own day.

Write in %s. At most three short lines, no greeting, no sign-off, no
encouragement. Say what to start with and why. Name at most one thing that could
wait or be dropped, and only if something obviously can.

These numbers are counted for you and are facts: %d due today, %d overdue. Do not
recount them, do not contradict them, and do not list every task back.`,
		language, due, overdue)

	return c.Complete(ctx, system, []Message{
		{Role: "user", Content: "The tasks:\n" + strings.Join(tasks, "\n")},
	})
}

// Ask svarer på et spørgsmål ud fra de tekststykker, den får med — og kun dem.
//
// Svaret skal bære sine kilder, og det er derfor svaret er JSON og ikke prosa: en
// model, der skriver "[3]" midt i en sætning, gør det af og til, og en, der
// afleverer nummeret i et felt, gør det hver gang. Fladen skal kunne lave et link,
// ikke lede efter kantede parenteser i en tekst.
//
// "Det står der ikke" er et rigtigt svar. En model, der hellere gætter end
// skuffer, er ubrugelig til netop det her: man spørger om sine egne noter, fordi
// man ikke selv kan huske det, og har derfor ingen mulighed for at se, at svaret
// var opfundet.
func (c *Client) Ask(ctx context.Context, question string, passages []string, locale string) (string, []int, error) {
	language := languageOf(locale)

	system := fmt.Sprintf(`You answer a question from somebody's own notes and tasks.

Use only the passages given. If they do not answer the question, say so plainly —
that is a useful answer, and a guess is not: the person is asking because they
cannot remember, so they cannot tell an invented answer from a real one.

Write in %s, at most four short lines. Reply with a JSON object and nothing else:
{"answer": "...", "sources": [1, 3]} where sources are the numbers of the
passages the answer actually rests on, and nothing else.`, language)

	prompt := "Question: " + question + "\n\nPassages:\n" + strings.Join(passages, "\n\n")

	reply, err := c.Complete(ctx, system, []Message{{Role: "user", Content: prompt}})
	if err != nil {
		return "", nil, err
	}

	var out struct {
		Answer  string `json:"answer"`
		Sources []int  `json:"sources"`
	}
	if err := json.Unmarshal([]byte(extractObject(reply)), &out); err != nil {
		// Et svar, der ikke er JSON, er stadig et svar. Teksten er det, personen
		// spurgte om; henvisningerne er det, der går tabt, og det er en dårligere
		// dag end en fejlmeddelelse ville være.
		return strings.TrimSpace(reply), nil, nil
	}
	return strings.TrimSpace(out.Answer), out.Sources, nil
}

// TasksFromMail skriver én opgavelinje pr. mail.
//
// Emnefeltet er skrevet til at blive genkendt i en indbakke, ikke til at blive
// gjort noget ved: "SV: SV: Vedr. levering uge 12" siger, hvilken tråd det er, og
// intet om, hvad man skal. Modellen skriver den handling, mailen beder om — og
// lader være, hvis den ikke beder om nogen.
func (c *Client) TasksFromMail(ctx context.Context, mails []string, today, locale string) ([]Suggestion, error) {
	language := languageOf(locale)

	system := fmt.Sprintf(`You turn emails into the tasks they ask for.

For each numbered email, write the line the recipient would have typed for
themselves — what they have to DO, in %s, starting with a verb. Keep the sender's
name in it when it matters who it is about. Not a summary of the email: the
action it asks for.

The line may carry a date in plain %s, and p1..p4 for priority, but only when the
email itself says so — "before friday" in the text is a date, an email that merely
sounds urgent is not. Today is %s.

Reply with a JSON array and nothing else — no prose, no code fence. Each element
is {"line": "N. ..."} keeping the number the email was given, so the line can be
put back on the right one. Leave an email out entirely if it asks for nothing.`,
		language, language, today)

	return c.suggestions(ctx, system, "Emails:\n"+strings.Join(mails, "\n"))
}

// suggestions er den fælles halvdel: spørg, læs svaret som JSON, og smid det væk,
// der ikke er en linje.
//
// Et tomt svar er ikke en fejl. "Der er ikke noget at foreslå" er et rigtigt svar
// på begge spørgsmål, og en fejlmeddelelse ville lære folk, at funktionen er i
// stykker, når den i virkeligheden var enig med dem.
func (c *Client) suggestions(ctx context.Context, system, prompt string) ([]Suggestion, error) {
	reply, err := c.Complete(ctx, system, []Message{{Role: "user", Content: prompt}})
	if err != nil {
		return nil, err
	}

	var out []Suggestion
	if err := json.Unmarshal([]byte(extractJSON(reply)), &out); err != nil {
		return nil, fmt.Errorf("ai: the model did not return a list: %s", truncate(reply, 200))
	}

	clean := make([]Suggestion, 0, len(out))
	for _, s := range out {
		s.Line = strings.TrimSpace(s.Line)
		s.Why = strings.TrimSpace(s.Why)
		if s.Line != "" {
			clean = append(clean, s)
		}
	}
	return clean, nil
}

// languageOf er det sprog, svaret skal skrives i. Ét sted, fordi det ellers er
// den slags, der bliver rettet ét sted ud af fire.
func languageOf(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		return "English"
	}
	return "Danish"
}

// unfence tager svaret ud af den kodeblok, modeller pakker det ind i, uanset hvor
// omhyggeligt de bliver bedt om at lade være.
func unfence(reply string) string {
	reply = strings.TrimSpace(reply)
	fence := strings.Index(reply, "```")
	if fence < 0 {
		return reply
	}
	rest := reply[fence+3:]
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[newline+1:]
	}
	if end := strings.Index(rest, "```"); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return reply
}

// extractObject er extractJSON for et objekt frem for en liste. Samme problem,
// samme løsning: modeller pakker svar ind i en kodeblok, hvor omhyggeligt de end
// bliver bedt om at lade være.
func extractObject(reply string) string {
	reply = unfence(reply)
	start := strings.IndexByte(reply, '{')
	end := strings.LastIndexByte(reply, '}')
	if start >= 0 && end > start {
		return reply[start : end+1]
	}
	return reply
}

// extractJSON pulls an array out of a reply that may have been wrapped in a code
// fence or preceded by a sentence, which models do however firmly they are asked
// not to.
func extractJSON(reply string) string {
	reply = unfence(reply)
	start := strings.IndexByte(reply, '[')
	end := strings.LastIndexByte(reply, ']')
	if start >= 0 && end > start {
		return reply[start : end+1]
	}
	return reply
}
