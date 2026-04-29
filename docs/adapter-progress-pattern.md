# Adapter progress pattern (reference)

This document describes a reusable **pattern** for user-I/O adapters that call a long-running backend (for example `POST /api/v1/chat` → runtime) while surfacing **coarse-grained** progress to the user. It is **not** a shared Go package; implementations stay in each adapter (Telegram, Slack, enterprise IM, etc.) and only follow the same lifecycle.

Progress is always derived from the same **canonical event stream**: orchestrator-proxied logger rows with `trace_id` and full `session_id` (for example `telegram_<chatKey>`), matching what runtime emits.

## Abstract lifecycle

1. **Begin** — Before or right after starting the upstream request, create a user-visible “anchor” for progress (placeholder message, typing indicator, WebSocket channel, etc.).
2. **Poll** — On a fixed interval, fetch recent events (for example `GET …/logger/events/recent?limit=120`), filter by `trace_id` and `session_id`, sort by stable event id, **dedupe** by id so each row is applied once.
3. **Reduce** — Map each accepted event to a **single line of “current status”** (not a chat transcript). Later events replace the conceptual “headline” for this turn.
4. **Flush** — Push the current headline to the channel with **rate limiting** (respect platform quotas and `Retry After` style signals).
5. **EndOK** — Stop polling; remove or hide the progress anchor; deliver the final assistant payload on the normal “reply” path.
6. **EndErr** — Stop polling; show the error **without** losing the last visible state. If polling uses a cancellable context, perform the **final** UI update **before** cancelling that context so the last edit is not aborted mid-flight.
7. **Degrade** — If the anchor cannot be created (network, permissions), skip all progress UI for that turn; the adapter must still complete the main request/response flow.

Shared building blocks worth reusing across adapters:

- **Reducer** — Pure function `LoggerEvent → (headline string, ok bool)` plus optional helpers (for example parsing `manage_user_docker` JSON `args` into a short human line).
- **Reducer hygiene** — Skip generic “thinking” placeholders that do not add information (for example `react_step_start` if it would only say “model thinking…”). Update the headline when something **observable** happens: tool calls, final text-only model response, errors, or explicit runtime phases. Prefer **stable machine-oriented labels** (tool name + literal `action` + key fields) over natural-language mapping so the project stays **language-neutral**; product-specific locales belong in a future i18n layer, not in the adapter reducer.
- **Correlation keys** — `trace_id` + `session_id` must match runtime/logger; local chat keys alone are wrong if the backend prefixes channel (see `telegram_<…>`).

## Variant A — Editable single message (Telegram)

**When to use:** The platform allows `editMessageText` (or equivalent) on a bot-owned message.

**Behavior in this repo (`adapter-telegram`):**

- Send one placeholder text message (“已收到，正在处理…”).
- Poll logger every **2s**; after applying new events, **replace** the placeholder text with the latest headline only if it changed (avoid redundant edits).
- **EndOK:** delete the placeholder, then send the final reply as a normal message.
- **EndErr:** edit the placeholder to the full error text; **do not** delete (user should keep reading the failure). If there was no placeholder, fall back to sending a new error message.
- **Rate limits:** Back off on retryable errors and honor Telegram `retry_after` when exposed by the client library.

## Variant B — No edit API (many enterprise IMs)

**When to use:** The channel does not allow editing bot messages (or edits are unreliable / forbidden).

**Recommended behavior:**

- Maintain the same **Poll + Reduce** loop internally.
- **Do not** rely on `editMessageText`. Instead, **buffer** headline updates and **emit at most one new chat message per aggregation window** (for example **every 10 seconds**), each message containing **only the latest headline** for that window (still “current step”, not a full log). If the headline did not change during a window, skip sending.
- **EndOK:** send nothing extra if the last aggregated message already matches the final phase; then send the **final assistant reply** as usual. Optionally send one short line such as “已完成” if the product wants closure before the long final body.
- **EndErr:** send **one** dedicated error message (same content policy as Variant A).
- **Degrade:** identical to the abstract lifecycle.

**Tuning:** Shorter windows (for example 5s) feel snappier but increase spam; longer windows (15–30s) reduce noise but feel laggy. 10s is a reasonable default for no-edit channels.

## Variant C — Streaming / Web (optional)

If the client keeps an open connection (SSE, WebSocket), **Flush** can push JSON `{ "phase", "headline" }` on each reducer change with server-side throttling (for example 500ms–2s), without chat message limits. **EndOK** closes the stream then returns the final body on the HTTP response or a follow-up event.

## Anti-patterns

- Appending every logger row as a new IM bubble (noisy; duplicates WebUI/logger).
- Writing progress lines into the **session** store (pollutes conversation history unless explicitly desired).
- Polling faster than the platform allows without backoff (429 loops).

## Reference implementation

Telegram: [adapter-telegram/cmd/server/main.go](../adapter-telegram/cmd/server/main.go) — `progressTracker`, `eventToProgressText`, `describeToolStart`.
