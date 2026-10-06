package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestContextPlanAndPreparedResult(t *testing.T) {
	var out bytes.Buffer
	r := &run{out: &out}
	p := &move.Plan{Kind: move.KindContinue, Title: "Fictional task", Agent: "Codex", Continue: &move.ContinuePlan{Report: convert.Report{Method: "vendor-import", Used: 1000, Budget: 19200, Capacity: ir.Capacity{Source: "unverified fallback"}, Archive: "/home/alice/archive.jsonl"}}}
	r.renderPlan(p)
	for _, want := range []string{"import input upper estimate 1000 / 19200", "unverified fallback", "/home/alice/archive.jsonl"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	out.Reset()
	r.renderResult(p, &move.Result{})
	if !strings.Contains(out.String(), "is prepared for Codex") || strings.Contains(out.String(), "continues in") {
		t.Fatal(out.String())
	}
}
