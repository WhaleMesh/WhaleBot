# Automated Batch Benchmarking (for AI agents)

This is a self-contained runbook for an AI agent (or a shell script) to benchmark
**multiple local GGUF models sequentially** against WhaleBot: load one model in
llama.cpp, register/activate it as an llm-openai profile, run the benchmark, unload,
repeat. Results accumulate in the `runtime_data` volume and are compared on the
WebUI Benchmark page (`#/benchmark`) or via `GET /api/v1/benchmark/runs`.

Read this file, collect the inputs in §1, then execute the loop in §3. No code
changes are required — everything runs over existing HTTP APIs.

## 1) Inputs you need from the user

- List of models: display name + GGUF path (or the llama.cpp launch command per model).
- llama.cpp port (below assumed `18081`, reachable from the host).
- Whether to include the E2E tier (`include_e2e`; adds several minutes per model,
  builds/runs real containers).
- Orchestrator base URL (default `http://localhost:18080`).

Preconditions:

- WhaleBot stack is up (`docker compose up`), `curl -s $ORCH/health` reachable.
- `LLM_INVOKE_TIMEOUT_SEC` in `.env` is generous for local models (≥180 recommended;
  first request after a model swap re-ingests the whole prompt).
- llama.cpp `llama-server` must be launched with `--jinja` — without it, tool calls
  are emitted as plain text instead of structured `tool_calls` and every tier of the
  benchmark degrades.

## 2) API surface (all via orchestrator, no auth)

| Purpose | Call |
|---|---|
| List LLM profiles | `GET /api/v1/llm-components/llm-openai/config` → `{config: {active_model_id, models: [{id, name, base_url, model}]}}` |
| Replace profiles (append = read-modify-write) | `PUT /api/v1/llm-components/llm-openai/config` body `{models: [{id, name, base_url, api_key, model}...], active_model_id}` |
| Activate a profile | `POST /api/v1/llm-components/llm-openai/active` body `{"id": "<profile_id>"}` |
| Chat-stack readiness | `GET /health` → wait for `chat_ready: true` |
| Start benchmark (async, single-flight) | `POST /api/v1/benchmark/run` body `{"include_e2e": true|false}` → `{run_id}`; `409` if a run is in progress, `503` if no active model |
| Poll / read results | `GET /api/v1/benchmark/runs` → `{runs: [...]}`; run `status`: `running` \| `completed` \| `failed` |
| Delete a run | `DELETE /api/v1/benchmark/runs/{id}` |

PUT semantics that make scripted append safe (`llm-openai/internal/configstore/store.go`):

- It is a **full replace**: always send the existing models plus the new one.
- `api_key: ""` on an existing entry **keeps its stored key** (merged by `id`), so the
  masked list from GET can be round-tripped verbatim.
- `active_model_id` **must be sent** — an empty value clears the active model.
- `id` and `name` must each be unique; `id`, `name`, `base_url`, `model` are required.
- llama.cpp needs no key; use any placeholder (e.g. `"none"`). Localhost-style
  `base_url` is fine — llm-openai rewrites it to `host.docker.internal` internally.

The benchmark always tests the **currently active** profile and stamps the run with
its `name`/`model`, so each local model needs its own profile for the history table
to be comparable.

## 3) Per-model loop

For each model, in order:

1. **Load**: start `llama-server -m <gguf> --port 18081 --jinja -ngl 99` (background,
   capture PID). Wait until `curl -sf http://localhost:18081/health` returns 200.
2. **Profile**: `GET …/llm-components/llm-openai/config`. If a profile with this
   model's name exists, `POST …/active {"id": …}`. Otherwise append it with one PUT
   (existing models with `api_key: ""` + new entry, `active_model_id` = new id).
3. **Readiness**: poll `GET $ORCH/health` until `chat_ready == true` (heartbeats are
   10 s apart; typically ready within ~15 s of activation).
4. **Run**: `POST …/benchmark/run` with the chosen `include_e2e`. On `409`, another
   run is still active — wait and retry. Poll `GET …/benchmark/runs` every ~10 s until
   this `run_id` leaves `running`. Simulated tier takes single-digit minutes;
   `include_e2e: true` adds real container builds (budget 10–20 min per model).
5. **Unload**: kill the llama-server PID and wait for it to exit before loading the
   next model (local VRAM fits one model at a time).

Report per model: `status`, `scores.total` (0–100 simulated), and `e2e.score` /
`e2e.status` when E2E ran. On `failed`, surface the run's `error` field.

## 4) Reference script

```bash
#!/usr/bin/env bash
set -euo pipefail
ORCH=${ORCH:-http://localhost:18080}
LLAMA_PORT=${LLAMA_PORT:-18081}
INCLUDE_E2E=${INCLUDE_E2E:-true}

# name -> gguf path (names become llm-openai profile names)
declare -A MODELS=(
  ["gemma-12b"]="/models/gemma-12b-it.Q5_K_M.gguf"
  ["qwen-14b"]="/models/qwen2.5-14b.Q4_K_M.gguf"
)

activate_profile() { # $1 = profile name; appends the profile if missing, then activates
  local name=$1 cfg pid body
  cfg=$(curl -s "$ORCH/api/v1/llm-components/llm-openai/config")
  pid=$(echo "$cfg" | jq -r --arg n "$name" '.config.models[] | select(.name==$n) | .id')
  if [ -z "$pid" ]; then
    pid="local-$name"
    body=$(echo "$cfg" | jq --arg id "$pid" --arg n "$name" --arg u "http://localhost:$LLAMA_PORT/v1" '
      {models: (.config.models | map({id, name, base_url, model, api_key: ""})
                + [{id: $id, name: $n, base_url: $u, model: $n, api_key: "none"}]),
       active_model_id: $id}')
    curl -sf -X PUT "$ORCH/api/v1/llm-components/llm-openai/config" \
         -H 'Content-Type: application/json' -d "$body" >/dev/null
  else
    curl -sf -X POST "$ORCH/api/v1/llm-components/llm-openai/active" \
         -H 'Content-Type: application/json' -d "{\"id\":\"$pid\"}" >/dev/null
  fi
}

for name in "${!MODELS[@]}"; do
  echo "=== $name ==="
  llama-server -m "${MODELS[$name]}" --port "$LLAMA_PORT" --jinja -ngl 99 &
  LLAMA_PID=$!
  until curl -sf "http://localhost:$LLAMA_PORT/health" >/dev/null; do sleep 2; done

  activate_profile "$name"
  until curl -s "$ORCH/health" | jq -e '.chat_ready' >/dev/null; do sleep 3; done

  RUN=$(curl -s -X POST "$ORCH/api/v1/benchmark/run" -H 'Content-Type: application/json' \
        -d "{\"include_e2e\":$INCLUDE_E2E}" | jq -r .run_id)
  while :; do
    ROW=$(curl -s "$ORCH/api/v1/benchmark/runs" | jq --arg id "$RUN" '.runs[] | select(.id==$id)')
    [ "$(echo "$ROW" | jq -r .status)" != "running" ] && break
    sleep 10
  done
  echo "$name: $(echo "$ROW" | jq -r '"status=\(.status) total=\(.scores.total) e2e=\(.e2e.score // "n/a")"')"

  kill "$LLAMA_PID"; wait "$LLAMA_PID" 2>/dev/null || true
done
echo "All done. Compare at http://localhost:18000/#/benchmark"
```

## 5) Reading results

Each run in `GET /api/v1/benchmark/runs` contains:

- `model_name` / `model` — profile name / upstream model id (your comparison key).
- `case_set` — benchmark case-set version; compare scores only within the same version.
- `scores` — `total` (0–100 daily-task readiness), its `plan_gate` / `tool_call` / `react` components, and `bonus` (0–100 capability-ceiling challenges; v3+ only).
- `metrics` — `llm_calls`, `avg_latency_ms`, token counts.
- `cases[]` — per-case `pass`/`score`/`detail` for failure analysis.
- `e2e` — `status` (`passed|failed|skipped|error`), `score` (0–100 from 5 checkpoints),
  `checkpoints[]`, `turn_replies[]`, `tool_events[]` for diagnosis.

History persists across restarts (`runtime_data` volume). The WebUI Benchmark page
renders the same data with per-run JSON download.

## 6) Failure modes

- `409` on start: a run is already in progress (single-flight). Wait, don't parallelize.
- `503 no active model`: activation didn't land — re-check step 2/3.
- Run `failed` with upstream timeout: raise `LLM_INVOKE_TIMEOUT_SEC` and restart
  `llm-openai` (`docker compose up -d llm-openai`). Each E2E chat turn already
  retries once, but repeated timeouts fail the run.
- Low `tool_call` score with tool-call text visible in `turn_replies`: the inference
  server is not emitting structured tool calls — verify `--jinja` (and that the GGUF
  ships a chat template with tool support).
- Stuck `running` after a crash: the run flips to `failed` on runtime restart; you
  can also `DELETE /api/v1/benchmark/runs/{id}`.
