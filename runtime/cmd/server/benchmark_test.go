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
