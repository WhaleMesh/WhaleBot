package main

import "testing"

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
