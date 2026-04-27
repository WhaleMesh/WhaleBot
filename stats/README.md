# stats

Optional aggregation service for Overview metrics. Owns a SQLite database at `STATS_DB_PATH` (default `/data/stats.db`).

## Registration

- `type`: `stats`
- `capabilities`: `stats_overview`, `stats_ingest`

## HTTP

- `GET /health`
- `POST /events` — batch ingest body:

```json
{
  "events": [
    { "kind": "message", "ts": "2026-04-27T09:00:00Z" },
    { "kind": "tool_call" },
    {
      "kind": "tokens",
      "prompt_tokens": 10,
      "completion_tokens": 20,
      "total_tokens": 30
    }
  ]
}
```

`kind` must be `message`, `tool_call`, or `tokens`. Omit `ts` to use server time (UTC). `meta` is optional object of string values (stored as JSON).

- `GET /stats/overview` — returns `window` (`start`, `end`, `label`) and `stats`:

  - `messages.{total,last_24h}` — row counts for `kind=message`
  - `tool_calls.{total,last_24h}` — row counts for `kind=tool_call`
  - `tokens.prompt|completion|total` each `{total,last_24h}` — sums of numeric columns on `kind=tokens` rows

Window: `end = now (UTC)`, `start = end truncated to the hour minus 24 hours` (rolling 24h aligned to the hour).

## Orchestrator

`GET /api/v1/stats/overview` proxies to the healthy `type=stats` component. If none is registered, returns `503` with `code: stats_disabled`.
