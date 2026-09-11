package main

import "testing"

func TestMergeLeadingSystemMessages(t *testing.T) {
	t.Parallel()
	msgs := []cmMessage{
		{Role: "system", Content: "base"},
		{Role: "system", Content: "plan"},
		{Role: "system", Content: "skills"},
	}
	got := mergeLeadingSystemMessages(msgs)
	if len(got) != 1 || got[0].Role != "system" || got[0].Content != "base\n\nplan\n\nskills" {
		t.Fatalf("unexpected merge: %+v", got)
	}
	// Single system message untouched; user turn preserved after merge.
	msgs = []cmMessage{
		{Role: "system", Content: "base"},
		{Role: "system", Content: "plan"},
		{Role: "user", Content: "hi"},
	}
	got = mergeLeadingSystemMessages(msgs)
	if len(got) != 2 || got[0].Content != "base\n\nplan" || got[1].Role != "user" {
		t.Fatalf("unexpected merge with user: %+v", got)
	}
	one := []cmMessage{{Role: "system", Content: "only"}, {Role: "user", Content: "hi"}}
	if out := mergeLeadingSystemMessages(one); len(out) != 2 || out[0].Content != "only" {
		t.Fatalf("single system should be untouched: %+v", out)
	}
}

func TestParsePlanGateResponse_valid(t *testing.T) {
	t.Parallel()
	d, ok := parsePlanGateResponse(`{"inject_plan_only":true,"restrict_mutating_tools":false}`)
	if !ok {
		t.Fatal("expected parsed ok")
	}
	if !d.InjectPlanOnly || d.RestrictMutatingTools {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestPlanConfirmationOverridesPlanOnlyDecision(t *testing.T) {
	t.Parallel()
	gate := planGateDecision{InjectPlanOnly: true, RestrictMutatingTools: true}
	if shouldForcePlanOnly(gate, true) {
		t.Fatal("confirmed plan must enable tools even when the classifier repeats inject_plan_only=true")
	}
	if !shouldForcePlanOnly(gate, false) {
		t.Fatal("unconfirmed execution must remain plan-only")
	}
}

func TestParsePlanGateResponse_defaultsWhenPartial(t *testing.T) {
	t.Parallel()
	d, ok := parsePlanGateResponse(`{"inject_plan_only":false}`)
	if !ok {
		t.Fatal("expected parsed ok")
	}
	if d.InjectPlanOnly {
		t.Fatal("inject should be false")
	}
	if !d.RestrictMutatingTools {
		t.Fatal("missing restrict field should keep conservative default true")
	}
}

func TestParsePlanGateResponse_markdownFence(t *testing.T) {
	t.Parallel()
	raw := "```json\n{\"inject_plan_only\": false, \"restrict_mutating_tools\": true}\n```"
	d, ok := parsePlanGateResponse(raw)
	if !ok {
		t.Fatal("expected parsed ok")
	}
	if d.InjectPlanOnly || !d.RestrictMutatingTools {
		t.Fatalf("unexpected %+v", d)
	}
}

func TestParsePlanGateResponse_thinkingModel(t *testing.T) {
	t.Parallel()
	raw := "<think>\nThe user wants to build a Go project, so this is substantive execution.\n</think>\nSure. {\"inject_plan_only\": true, \"restrict_mutating_tools\": true}"
	d, ok := parsePlanGateResponse(raw)
	if !ok {
		t.Fatal("expected parsed ok")
	}
	if !d.InjectPlanOnly || !d.RestrictMutatingTools {
		t.Fatalf("unexpected %+v", d)
	}
	// Unclosed think block with no JSON after it -> parse failure, conservative default.
	if _, ok := parsePlanGateResponse("<think>still reasoning when max_tokens hit"); ok {
		t.Fatal("truncated think-only output should not parse")
	}
}

func TestNormalizeDockerToolCall(t *testing.T) {
	t.Parallel()
	// Alias with explicit action passes through under canonical name.
	name, args := normalizeDockerToolCall("docker_files", `{"action":"read_file","name":"c1","path":"/workspace/a.go"}`)
	if name != "manage_user_docker" {
		t.Fatalf("got name %q", name)
	}
	if parseDockerActionFromArgs(args) != "read_file" {
		t.Fatalf("action lost: %s", args)
	}
	// Default action injected when omitted.
	name, args = normalizeDockerToolCall("export_artifact", `{"name":"c1","path":"/workspace/out.tar.gz"}`)
	if name != "manage_user_docker" || parseDockerActionFromArgs(args) != "export_artifact" {
		t.Fatalf("got %q / %s", name, args)
	}
	name, args = normalizeDockerToolCall("docker_exec", `{"name":"c1","command_sh":"go build ./..."}`)
	if name != "manage_user_docker" || parseDockerActionFromArgs(args) != "exec" {
		t.Fatalf("got %q / %s", name, args)
	}
	// Non-alias tools untouched.
	if name, _ := normalizeDockerToolCall("list_secrets", `{}`); name != "list_secrets" {
		t.Fatalf("got %q", name)
	}
	// Legacy canonical name untouched.
	if name, _ := normalizeDockerToolCall("manage_user_docker", `{"action":"list"}`); name != "manage_user_docker" {
		t.Fatalf("got %q", name)
	}
	// Malformed args must not panic and still route to canonical dispatch.
	if name, _ := normalizeDockerToolCall("docker_lifecycle", `{`); name != "manage_user_docker" {
		t.Fatalf("got %q", name)
	}
	if name, _ := normalizeDockerToolCall("docker_exec", `null`); name != "manage_user_docker" {
		t.Fatalf("got %q", name)
	}
}

func TestParsePlanGateResponse_invalid(t *testing.T) {
	t.Parallel()
	d, ok := parsePlanGateResponse("not json")
	if ok {
		t.Fatal("expected parse failure")
	}
	def := conservativePlanGateDefault()
	if d != def {
		t.Fatalf("expected conservative default %+v got %+v", def, d)
	}
}

func TestIsHighRiskDockerAction(t *testing.T) {
	t.Parallel()
	// Always high-risk regardless of args.
	for _, a := range []string{"remove", "delete_file", "pull_image"} {
		if !isHighRiskDockerAction(a, `{}`) {
			t.Fatalf("%q should be high risk", a)
		}
	}
	// create with framework/default image is low risk (fluid).
	if isHighRiskDockerAction("create", `{"image":"whalebot/userdocker-golang:latest"}`) {
		t.Fatal("framework image create should not be high risk")
	}
	if isHighRiskDockerAction("create", `{}`) {
		t.Fatal("default image create should not be high risk")
	}
	// create with external image is high risk.
	if !isHighRiskDockerAction("create", `{"image":"python:3.12"}`) {
		t.Fatal("external image create should be high risk")
	}
	// Routine mutations stay fluid.
	for _, a := range []string{"exec", "write_file", "start", "export_artifact"} {
		if isHighRiskDockerAction(a, `{}`) {
			t.Fatalf("%q should not be high risk", a)
		}
	}
}

func TestParseDockerActionFromArgs(t *testing.T) {
	t.Parallel()
	if g := parseDockerActionFromArgs(`{"action":"LIST","name":"x"}`); g != "list" {
		t.Fatalf("got %q", g)
	}
	if parseDockerActionFromArgs(`{`) != "" {
		t.Fatal("invalid json should yield empty action")
	}
}

func TestShouldForcePlanFirst_legacyKeywordProbe(t *testing.T) {
	t.Parallel()
	h := []sessionMessage{}
	if shouldForcePlanFirst("测试", h) {
		t.Fatal("bare 测试 should not force plan-first under legacy heuristics")
	}
	if !shouldForcePlanFirst("请执行部署", h) {
		t.Fatal("执行 should still trigger legacy plan-first")
	}
}
