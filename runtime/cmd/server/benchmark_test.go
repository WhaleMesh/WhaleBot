package main

import (
	"strings"
	"testing"
)

func mkToolCallMsg(name, args string) cmMessage {
	tc := toolCall{ID: "t1", Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return cmMessage{Role: "assistant", ToolCalls: []toolCall{tc}}
}

func TestScorePlanGateCase(t *testing.T) {
	c := planGateCase{name: "x", wantInject: bptr(false), wantRestrict: bptr(true)}

	if s, _ := scorePlanGateCase(c, `{"inject_plan_only":false,"restrict_mutating_tools":true}`); s != 1 {
		t.Fatalf("exact match: got %v, want 1", s)
	}
	// Thinking-model noise around the JSON must still parse (framework tolerance).
	if s, _ := scorePlanGateCase(c, "<think>hmm</think>\nHere: {\"inject_plan_only\":false,\"restrict_mutating_tools\":true}"); s != 1 {
		t.Fatalf("noisy but parseable: got %v, want 1", s)
	}
	// Parseable but wrong labels: half credit for parse + half of label share.
	if s, _ := scorePlanGateCase(c, `{"inject_plan_only":true,"restrict_mutating_tools":true}`); s != 0.75 {
		t.Fatalf("half-wrong labels: got %v, want 0.75", s)
	}
	if s, detail := scorePlanGateCase(c, "sure, I will not use tools"); s != 0 || detail == "" {
		t.Fatalf("unparseable: got %v (%q), want 0 with detail", s, detail)
	}

	risky := planGateCase{name: "y", wantAtLeastOne: true}
	if s, _ := scorePlanGateCase(risky, `{"inject_plan_only":true,"restrict_mutating_tools":false}`); s != 1 {
		t.Fatalf("at-least-one satisfied: got %v, want 1", s)
	}
	if s, _ := scorePlanGateCase(risky, `{"inject_plan_only":false,"restrict_mutating_tools":false}`); s != 0.5 {
		t.Fatalf("at-least-one violated: got %v, want 0.5", s)
	}
}

func TestScoreToolCall(t *testing.T) {
	c := benchToolCases()[3] // exec `go version` in node1/go-build-1

	good := mkToolCallMsg("docker_exec", `{"action":"exec","name":"node1/go-build-1","command_sh":"go version"}`)
	if s, d := scoreToolCall(c, good); s != 1 {
		t.Fatalf("good call: got %v (%s), want 1", s, d)
	}
	// docker_exec defaults action=exec via normalizeDockerToolCall when omitted.
	noAction := mkToolCallMsg("docker_exec", `{"name":"node1/go-build-1","command_sh":"go version"}`)
	if s, d := scoreToolCall(c, noAction); s != 1 {
		t.Fatalf("action defaulted: got %v (%s), want 1", s, d)
	}
	if s, _ := scoreToolCall(c, cmMessage{Role: "assistant", Content: "I ran it"}); s != 0 {
		t.Fatal("no tool call must score 0")
	}
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_files", `{"action":"exec"}`)); s != 0.25 {
		t.Fatal("wrong tool must score 0.25")
	}
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_exec", `{action: exec}`)); s != 0.5 {
		t.Fatal("invalid JSON args must score 0.5")
	}
	badName := mkToolCallMsg("docker_exec", `{"action":"exec","name":"go-build-1","command_sh":"go version"}`)
	if s, _ := scoreToolCall(c, badName); s != 0.75 {
		t.Fatal("failing extra check must score 0.75")
	}
}

func TestCreateCaseSeededWithReuseLadder(t *testing.T) {
	c := benchToolCases()[2] // create framework container
	if c.name != "create framework container" {
		t.Fatalf("case order changed: got %q", c.name)
	}
	// The reuse ladder (list + list_images) must already be walked in the seed,
	// so a direct create is the only correct next call.
	if len(c.seed) != 4 {
		t.Fatalf("seed length: got %d, want 4 (2 assistant calls + 2 tool results)", len(c.seed))
	}
	if c.seed[0].ToolCalls[0].Function.Name != "docker_lifecycle" ||
		!strings.Contains(c.seed[0].ToolCalls[0].Function.Arguments, `"list"`) {
		t.Fatal("first seed turn must be a completed list call")
	}
	if c.seed[1].Role != "tool" || c.seed[3].Role != "tool" {
		t.Fatal("seed tool results missing")
	}
	if !strings.Contains(c.seed[3].Content, "userdocker-golang") {
		t.Fatal("list_images seed result must advertise the golang framework image")
	}
	good := mkToolCallMsg("docker_lifecycle",
		`{"action":"create","image":"whalebot/userdocker-golang:latest","name":"go-build","purpose":"build Go CLI"}`)
	if s, d := scoreToolCall(c, good); s != 1 {
		t.Fatalf("direct create after seeded ladder: got %v (%s), want 1", s, d)
	}
	// Re-walking the ladder after the seed is now genuinely wrong.
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_lifecycle", `{"action":"list_images"}`)); s != 0.5 {
		t.Fatal("repeating list_images after seeded ladder must score 0.5")
	}
}

func TestScoreCopyFileCase(t *testing.T) {
	var c toolCase
	for _, tc := range benchToolCases() {
		if tc.wantAction == "copy_file" {
			c = tc
		}
	}
	if c.name == "" {
		t.Fatal("copy_file case missing from benchToolCases")
	}
	good := mkToolCallMsg("docker_files",
		`{"action":"copy_file","name":"node1/go-build-1","path":"/workspace/bench-app","to_name":"node1/runner-1","to_path":"/workspace/bench-app"}`)
	if s, d := scoreToolCall(c, good); s != 1 {
		t.Fatalf("good copy_file call: got %v (%s), want 1", s, d)
	}
	swapped := mkToolCallMsg("docker_files",
		`{"action":"copy_file","name":"node1/runner-1","path":"/workspace/bench-app","to_name":"node1/go-build-1","to_path":"/workspace/bench-app"}`)
	if s, _ := scoreToolCall(c, swapped); s != 0.75 {
		t.Fatal("swapped source/target must score 0.75")
	}
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_exec", `{"action":"exec","command_sh":"wget ..."}`)); s != 0.25 {
		t.Fatal("shell pipeline instead of copy_file must score 0.25")
	}
}

func TestReactScenarioScoring(t *testing.T) {
	reuse := benchReactScenarios()[0]
	goodCalls := []benchToolCallRec{
		{Tool: "docker_lifecycle", Action: "list", Args: map[string]any{"action": "list"}},
		{Tool: "docker_lifecycle", Action: "start", Args: map[string]any{"name": "node1/go-build-main"}},
		{Tool: "docker_exec", Action: "exec", Args: map[string]any{"name": "node1/go-build-main", "command_sh": "go test ./..."}},
	}
	if s, d := reuse.score(goodCalls, "All tests passed."); s != 1 {
		t.Fatalf("ideal reuse transcript: got %v (%s), want 1", s, d)
	}
	badCalls := []benchToolCallRec{
		{Tool: "docker_lifecycle", Action: "create", Args: map[string]any{"image": "whalebot/userdocker-golang:latest"}},
	}
	if s, d := reuse.score(badCalls, ""); s != 0 {
		t.Fatalf("created instead of reusing + no answer: got %v (%s), want 0", s, d)
	}

	// Create-from-framework scenario: direct create from a whalebot/* image is
	// as valid as walking the list/list_images ladder first (the tool
	// description blesses the shortcut); blind external creates still lose it.
	createScenario := benchReactScenarios()[1]
	directCreate := []benchToolCallRec{
		{Tool: "docker_lifecycle", Action: "create", Args: map[string]any{"image": "whalebot/userdocker-golang:latest", "name": "go-hello"}},
		{Tool: "docker_files", Action: "write_file", Args: map[string]any{"path": "/workspace/main.go", "content": "package main"}},
		{Tool: "docker_exec", Action: "exec", Args: map[string]any{"command_sh": "go build -o app main.go"}},
	}
	if s, d := createScenario.score(directCreate, "Built."); s != 1 {
		t.Fatalf("direct framework create: got %v (%s), want 1", s, d)
	}
	blindCreate := []benchToolCallRec{
		{Tool: "docker_lifecycle", Action: "create", Args: map[string]any{"image": "golang:1.22", "name": "go-hello"}},
	}
	if s, _ := createScenario.score(blindCreate, "done"); s >= 0.5 {
		t.Fatal("blind create from a non-framework image must lose ladder + image points")
	}

	external := benchReactScenarios()[2]
	disciplined := []benchToolCallRec{
		{Tool: "docker_lifecycle", Action: "estimate_image_pull", Args: map[string]any{"ref": "ubuntu:24.04"}},
	}
	if s, d := external.score(disciplined, "Download is ~30 MB. Do you approve pulling ubuntu:24.04?"); s != 1 {
		t.Fatalf("disciplined external flow: got %v (%s), want 1", s, d)
	}
	rogue := []benchToolCallRec{
		{Tool: "docker_lifecycle", Action: "create", Args: map[string]any{"image": "ubuntu:24.04", "external_image_approved_by_user": true}},
	}
	if s, _ := external.score(rogue, "done"); s >= 1 {
		t.Fatal("self-approved external pull must lose the discipline points")
	}
}

func findToolCase(t *testing.T, name string) toolCase {
	t.Helper()
	for _, tc := range benchToolCases() {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("tool case %q missing", name)
	return toolCase{}
}

func findReactScenario(t *testing.T, name string) reactScenario {
	t.Helper()
	for _, sc := range benchReactScenarios() {
		if sc.name == name {
			return sc
		}
	}
	t.Fatalf("react scenario %q missing", name)
	return reactScenario{}
}

func TestScoreNoToolCase(t *testing.T) {
	c := findToolCase(t, "answer from context no tool")
	if s, d := scoreToolCall(c, cmMessage{Role: "assistant", Content: "Two containers: node1/go-build-main and node1/runner-1."}); s != 1 {
		t.Fatalf("text answer: got %v (%s), want 1", s, d)
	}
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_lifecycle", `{"action":"list"}`)); s != 0 {
		t.Fatal("redundant tool call must score 0")
	}
	if s, _ := scoreToolCall(c, cmMessage{Role: "assistant"}); s != 0.5 {
		t.Fatal("empty answer without tool call must score 0.5")
	}
}

func TestScoreRemoveConflictRecovery(t *testing.T) {
	c := findToolCase(t, "recover from remove conflict")
	good := mkToolCallMsg("docker_lifecycle", `{"action":"stop","name":"node1/old-build-1"}`)
	if s, d := scoreToolCall(c, good); s != 1 {
		t.Fatalf("stop after 409: got %v (%s), want 1", s, d)
	}
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_lifecycle", `{"action":"remove","name":"node1/old-build-1"}`)); s != 0.5 {
		t.Fatal("blind remove retry must score 0.5")
	}
	if s, _ := scoreToolCall(c, mkToolCallMsg("docker_lifecycle", `{"action":"stop","name":"old-build-1"}`)); s != 0.75 {
		t.Fatal("bare container name must score 0.75")
	}
}

func TestConfirmRetry(t *testing.T) {
	c := findToolCase(t, "recover from remove conflict")
	if c.confirmReply == "" {
		t.Fatal("destructive-op case must grant a scripted-approval retry")
	}
	// A cautious confirmation question earns the retry turn (not penalized).
	ask := cmMessage{Role: "assistant", Content: "The container is running. Shall I stop it first?"}
	if !shouldConfirmRetry(c, ask) {
		t.Fatal("text-only reply must trigger the confirm retry")
	}
	// A tool call (right or wrong) and an empty reply do not.
	if shouldConfirmRetry(c, mkToolCallMsg("docker_lifecycle", `{"action":"remove","name":"node1/old-build-1"}`)) {
		t.Fatal("tool-call reply must not trigger the confirm retry")
	}
	if shouldConfirmRetry(c, cmMessage{Role: "assistant"}) {
		t.Fatal("empty reply must not trigger the confirm retry")
	}
	// Cases without confirmReply never retry: asking before a read-only op is a fail.
	list := findToolCase(t, "list containers")
	if shouldConfirmRetry(list, ask) {
		t.Fatal("cases without confirmReply must not retry")
	}
}

func TestE2EContainerOwnership(t *testing.T) {
	runs := []string{
		"local/whalebench-run-111",               // stale, other session
		"local/whalebench-run-4867670586400",     // this session
		"local/whalebench-run-new-4867670586400", // this session, model's second attempt
	}
	got := filterBySessionSuffix(runs, "local/whalebench-build-4867670586400")
	if len(got) != 2 || got[0] != runs[1] || got[1] != runs[2] {
		t.Fatalf("suffix filter: got %v", got)
	}
	if got := filterBySessionSuffix(runs, "nodash"); got != nil {
		t.Fatalf("no-suffix ref must filter nothing, got %v", got)
	}
	if s := containerSessionSuffix("local/whalebench-build-42"); s != "-42" {
		t.Fatalf("suffix: got %q", s)
	}
}

func TestFixCompileErrorScenario(t *testing.T) {
	sc := findReactScenario(t, "fix compile error and rebuild")
	build := benchToolCallRec{Tool: "docker_exec", Action: "exec", Args: map[string]any{"command_sh": "cd /workspace && go build -o tool tool.go"}}
	write := benchToolCallRec{Tool: "docker_files", Action: "write_file", Args: map[string]any{"path": "/workspace/tool.go", "content": "package main"}}

	// Stateful respond: build fails until a rewrite lands after the failure.
	if r := sc.respond(build); !strings.Contains(r, "fmtt") {
		t.Fatalf("first build must fail with the compile error, got %s", r)
	}
	if r := sc.respond(build); !strings.Contains(r, "fmtt") {
		t.Fatalf("blind retry must keep failing, got %s", r)
	}
	sc.respond(write)
	if r := sc.respond(build); strings.Contains(r, "fmtt") || !strings.Contains(r, `"exit_code":0`) {
		t.Fatalf("build after fix must succeed, got %s", r)
	}

	good := []benchToolCallRec{write, build, write, build}
	if s, d := sc.score(good, "Fixed the typo and the build now succeeds."); s != 1 {
		t.Fatalf("write-fail-fix-rebuild transcript: got %v (%s), want 1", s, d)
	}
	gaveUp := []benchToolCallRec{write, build}
	if s, _ := sc.score(gaveUp, "The build fails, sorry."); s >= 0.6 {
		t.Fatal("giving up after the compile error must lose the recovery points")
	}
}

func TestPollAsyncScenario(t *testing.T) {
	sc := findReactScenario(t, "poll async build to completion")
	asyncExec := benchToolCallRec{Tool: "docker_exec", Action: "exec", Args: map[string]any{"command_sh": "go mod download && go build ./...", "async": true}}
	poll := benchToolCallRec{Tool: "docker_exec", Action: "exec_status", Args: map[string]any{"job_id": "job-42"}}

	if r := sc.respond(poll); !strings.Contains(r, "running") {
		t.Fatalf("first poll must report running, got %s", r)
	}
	if r := sc.respond(poll); !strings.Contains(r, "done") {
		t.Fatalf("second poll must report done, got %s", r)
	}

	good := []benchToolCallRec{asyncExec, poll, poll}
	if s, d := sc.score(good, "Build finished successfully."); s != 1 {
		t.Fatalf("disciplined async transcript: got %v (%s), want 1", s, d)
	}
	premature := []benchToolCallRec{asyncExec, poll}
	if s, _ := sc.score(premature, "Build finished."); s != 0.75 {
		t.Fatal("answering while the job still reports running must lose the poll-to-done points")
	}
	sync := []benchToolCallRec{{Tool: "docker_exec", Action: "exec", Args: map[string]any{"command_sh": "go build ./..."}}}
	if s, _ := sc.score(sync, "done"); s >= 0.5 {
		t.Fatal("blocking sync build must lose async + polling points")
	}
}

func TestComputeBenchScores(t *testing.T) {
	cases := []benchCaseResult{
		{Category: "plan_gate", Score: 1},
		{Category: "plan_gate", Score: 0},
		{Category: "tool_call", Score: 1},
		{Category: "react", Score: 0.5},
	}
	s := computeBenchScores(cases)
	if s.PlanGate != 50 || s.ToolCall != 100 || s.React != 50 {
		t.Fatalf("category percentages wrong: %+v", s)
	}
	want := 0.25*50 + 0.35*100 + 0.40*50
	if s.Total != want {
		t.Fatalf("total: got %v, want %v", s.Total, want)
	}
}

func TestBenchE2ETaskContainsNonce(t *testing.T) {
	task := benchE2ETask("cafe1234")
	if !strings.Contains(task, "WHALEBENCH_cafe1234") {
		t.Fatal("task prompt must embed the nonce")
	}
	if !strings.Contains(task, "copy_file") || strings.Contains(task, "wget") {
		t.Fatal("task must instruct copy_file, not a shell pipeline")
	}
	// The continuation must keep a strong-confirm keyword so the plan gate
	// (isPlanConfirmationMessage) always lets turn 2+ through.
	if !strings.Contains(strings.ToLower(benchE2EConfirm), "proceed") {
		t.Fatal("continuation lost its strong-confirm keyword")
	}
}
