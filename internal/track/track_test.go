package track

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `# Migration tracker

Resource: azurerm_orchestrated_virtual_machine_scale_set

| No. | Path | Type | Required | Status | Proof Doc |
|-----|------|------|----------|--------|-----------|
| 1 | name | Argument | Yes | ✅ Completed | [1.name.md](1.name.md) |
| 2 | location | Argument | Yes | Pending |  |
| 3 | identity | Block | No | Pending for check |  |
| 4 | identity.type | Argument | No | In Progress |  |
| 5 | __check_root_hidden_fields__ | Argument | No | Failed |  |
`

func TestParseAndSummarize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "track.md")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 tasks, got %d (%+v)", len(got), got)
	}
	if got[0].Status != StatusCompleted {
		t.Errorf("task 1 status = %q, want completed", got[0].Status)
	}
	if got[1].Status != StatusPending || !got[1].IsArgument() || !got[1].IsRoot() {
		t.Errorf("task 2 unexpected: %+v", got[1])
	}
	if got[2].Status != StatusPendingForCheck || !got[2].IsBlock() {
		t.Errorf("task 3 unexpected: %+v", got[2])
	}
	if got[3].IsRoot() {
		t.Errorf("task 4 (identity.type) should not be root")
	}
	if got[4].Status != StatusFailed {
		t.Errorf("task 5 status = %q, want Failed", got[4].Status)
	}

	c := Summarize(got)
	if c.Total != 5 || c.Completed != 1 || c.Pending != 1 || c.PendingForChk != 1 || c.InProgress != 1 || c.Failed != 1 {
		t.Errorf("summary unexpected: %+v", c)
	}

	next, ok := NextActionable(got)
	if !ok || next.No != 2 {
		t.Errorf("NextActionable: got=%+v ok=%v, want task 2", next, ok)
	}
}
