package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lonegunmanb/avm2azapi/internal/prompts"
	"github.com/lonegunmanb/avm2azapi/internal/runner"
	"github.com/lonegunmanb/avm2azapi/internal/testcases"
	"github.com/lonegunmanb/avm2azapi/internal/track"
)

const (
	defaultCheckpointFile = ".migrate-checkpoint.json"
	defaultAccTestDir     = "azurermacctest"
)

// ---------------------------------------------------------------------------
// Checkpoint persistence (mirrors the JSON written by the original migrate.ps1)
// ---------------------------------------------------------------------------

func (o *Orchestrator) checkpointPath() string {
	name := o.cfg.CheckpointFile
	if name == "" {
		name = defaultCheckpointFile
	}
	return filepath.Join(o.cfg.WorkDir, name)
}

func (o *Orchestrator) loadCheckpoint() (map[string]bool, error) {
	cp := map[string]bool{}
	data, err := os.ReadFile(o.checkpointPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cp, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("parse checkpoint: %w", err)
	}
	return cp, nil
}

func (o *Orchestrator) saveCheckpoint(cp map[string]bool) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(o.checkpointPath(), data, 0o644)
}

func (o *Orchestrator) clearCheckpoint() error {
	return os.Remove(o.checkpointPath())
}

func (o *Orchestrator) accTestDir() string {
	if o.cfg.AccTestDir != "" {
		return filepath.Join(o.cfg.WorkDir, o.cfg.AccTestDir)
	}
	return filepath.Join(o.cfg.WorkDir, defaultAccTestDir)
}

// ---------------------------------------------------------------------------
// runLocalJob — track non-Copilot work in the same JobView pipeline so the
// TUI displays it consistently with Copilot sessions.
// ---------------------------------------------------------------------------

// emitFunc is passed to runLocalJob handlers to stream progress lines into
// the job's output buffer.
type emitFunc func(kind runner.EventKind, text string)

func (o *Orchestrator) runLocalJob(_ context.Context, id, title, stage string, fn func(emitFunc) error) error {
	job := runner.Job{ID: id, Title: title, Stage: stage}
	view := &JobView{Job: job, State: JobRunning, Started: time.Now()}
	o.mu.Lock()
	o.jobs = append(o.jobs, view)
	o.byID[id] = view
	o.active = id
	o.mu.Unlock()
	o.publish()

	emit := func(kind runner.EventKind, text string) {
		o.appendOutput(view, runner.EventLine{When: time.Now(), Kind: kind, Text: text})
		o.publish()
	}

	err := fn(emit)

	o.mu.Lock()
	view.Finished = time.Now()
	if err != nil {
		view.State = JobFailed
		view.Err = err
	} else {
		view.State = JobSucceeded
	}
	o.active = ""
	o.mu.Unlock()
	o.publish()
	return err
}

// recordSkippedStage adds a single skipped marker to the job list so the TUI
// reflects that a checkpoint short-circuited the stage.
func (o *Orchestrator) recordSkippedStage(stage string) {
	o.recordSkipped(runner.Job{
		ID:    "skip-" + stage,
		Title: "(checkpoint) skip " + stage,
		Stage: stage,
	})
}

// ---------------------------------------------------------------------------
// New stages
// ---------------------------------------------------------------------------

// runStageNewres invokes the external `newres` CLI to scaffold the initial
// AzAPI resource files.
func (o *Orchestrator) runStageNewres(ctx context.Context) error {
	o.setStage("newres")
	if o.cfg.ResourceType == "" {
		return fmt.Errorf("newres requires ResourceType")
	}
	return o.runLocalJob(ctx, "newres", "newres — scaffold AzAPI files", "newres", func(emit emitFunc) error {
		args := []string{"-r", o.cfg.ResourceType, "-dir", ".", "-variable-prefix="}
		emit(runner.EventInfo, "newres "+strings.Join(args, " "))

		cmd := exec.CommandContext(ctx, "newres", args...)
		cmd.Dir = o.cfg.WorkDir
		out, err := cmd.CombinedOutput()
		for _, line := range splitLines(string(out)) {
			emit(runner.EventTool, line)
		}
		if err != nil {
			return fmt.Errorf("newres: %w", err)
		}
		emit(runner.EventDone, "newres complete")
		return nil
	})
}

// runStageValidate ensures every row in track.md is marked Completed before
// proceeding. This matches the hard-fail check in migrate.ps1.
func (o *Orchestrator) runStageValidate(ctx context.Context) error {
	o.setStage("validate")
	return o.runLocalJob(ctx, "validate-track", "Validate track.md completion", "validate", func(emit emitFunc) error {
		trackPath := filepath.Join(o.cfg.WorkDir, "track.md")
		tasks, err := track.Parse(trackPath)
		if err != nil {
			return err
		}
		var bad []string
		for _, t := range tasks {
			if t.Status != track.StatusCompleted {
				bad = append(bad, fmt.Sprintf("Task #%d (%s) status=%q", t.No, t.Path, t.Status))
			}
		}
		if len(bad) > 0 {
			for _, b := range bad {
				emit(runner.EventError, b)
			}
			return fmt.Errorf("%d task(s) in track.md not completed", len(bad))
		}
		emit(runner.EventDone, fmt.Sprintf("all %d tasks completed", len(tasks)))
		return nil
	})
}

// runStageDeleteMain removes the original AzureRM main.tf.
func (o *Orchestrator) runStageDeleteMain(ctx context.Context) error {
	o.setStage("delete_main")
	return o.runLocalJob(ctx, "delete-main", "Delete original main.tf", "delete_main", func(emit emitFunc) error {
		path := filepath.Join(o.cfg.WorkDir, "main.tf")
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				emit(runner.EventInfo, "main.tf already absent")
				return nil
			}
			return err
		}
		emit(runner.EventDone, "removed "+path)
		return nil
	})
}

// runStageExtractTests iterates test_cases.md and runs one Copilot session
// per Pending row to extract + convert the test case (mirrors test_extractor.ps1).
func (o *Orchestrator) runStageExtractTests(ctx context.Context) error {
	o.setStage("extract_tests")
	tcFile := filepath.Join(o.cfg.WorkDir, "test_cases.md")
	cases, err := testcases.Parse(tcFile)
	if err != nil {
		return err
	}
	pending := 0
	for _, tc := range cases {
		if tc.ExtractionStatus != testcases.StatusPending {
			continue
		}
		pending++
		if uErr := testcases.UpdateExtractionStatus(tcFile, tc.Name, testcases.StatusInProgress); uErr != nil {
			// Non-fatal; just record.
			_ = uErr
		}
		job := runner.Job{
			ID:     "extract-" + tc.Name,
			Title:  "Extract test: " + tc.Name,
			Stage:  "extract_tests",
			Prompt: prompts.ExtractTestCase(tc.Name),
		}
		r, err := o.runJob(ctx, job)
		if err != nil {
			return err
		}
		newStatus := testcases.StatusCompleted
		if !r.OK {
			newStatus = testcases.StatusPending
		}
		if uErr := testcases.UpdateExtractionStatus(tcFile, tc.Name, newStatus); uErr != nil {
			_ = uErr
		}
		if !r.OK {
			return fmt.Errorf("extract failed for case %s", tc.Name)
		}
	}
	if pending == 0 {
		o.recordSkippedStage("extract_tests-empty")
	}
	return nil
}

// runStageDedupTests deduplicates extracted cases by MD5 of azurerm.tf+main.tf.
func (o *Orchestrator) runStageDedupTests(ctx context.Context) error {
	o.setStage("dedup_tests")
	return o.runLocalJob(ctx, "dedup-tests", "Deduplicate test cases", "dedup_tests", func(emit emitFunc) error {
		tcFile := filepath.Join(o.cfg.WorkDir, "test_cases.md")
		accDir := o.accTestDir()
		if _, err := os.Stat(accDir); err != nil {
			emit(runner.EventInfo, "no acceptance-test directory at "+accDir+" — skipping")
			return nil
		}
		log := func(s string) { emit(runner.EventTool, s) }
		res, err := testcases.Deduplicate(tcFile, accDir, false, log)
		if err != nil {
			return err
		}
		emit(runner.EventDone, fmt.Sprintf("unique=%d duplicates=%d errors=%d", res.Unique, res.Duplicates, res.Errors))
		return nil
	})
}

// runStageRunTests executes acceptance tests by running one Copilot session
// per eligible row in test_cases.md (mirrors run-all-acctests.ps1).
func (o *Orchestrator) runStageRunTests(ctx context.Context) error {
	o.setStage("run_tests")
	tcFile := filepath.Join(o.cfg.WorkDir, "test_cases.md")
	cases, err := testcases.Parse(tcFile)
	if err != nil {
		return err
	}

	// Initialise the test-phase progress counters.
	var toRun []testcases.TestCase
	for _, tc := range cases {
		if shouldRunTest(tc) {
			toRun = append(toRun, tc)
		}
	}
	o.setProgressTests(len(toRun), 0)

	breakFile := filepath.Join(o.cfg.WorkDir, "break")
	for _, tc := range toRun {
		job := runner.Job{
			ID:     "test-" + tc.Name,
			Title:  "Run test: " + tc.Name,
			Stage:  "run_tests",
			Prompt: prompts.RunAccTest(tc.Name),
		}
		if _, err := o.runJob(ctx, job); err != nil {
			return err
		}
		o.incProgressTests()
		// A non-OK result is logged inside the job view; we deliberately do
		// not abort the whole pipeline for one failing test (matches the
		// original ps1 prompt "do you want to continue?" defaulting yes when
		// in non-interactive mode).
		if _, err := os.Stat(breakFile); err == nil {
			return nil
		}
	}
	return nil
}

// runStageFinalCheck surfaces warning.md if it exists.
func (o *Orchestrator) runStageFinalCheck(ctx context.Context) error {
	o.setStage("final_check")
	return o.runLocalJob(ctx, "final-check", "Final verification", "final_check", func(emit emitFunc) error {
		warning := filepath.Join(o.cfg.WorkDir, "warning.md")
		if _, err := os.Stat(warning); err == nil {
			emit(runner.EventError, "WARNING: tasks may not be properly implemented — review warning.md")
		} else {
			emit(runner.EventDone, "no warning.md present")
		}
		return nil
	})
}

// shouldRunTest replicates the filter in run-all-acctests.ps1's normal mode:
// skip cases whose extraction or test status contains "invalid", whose
// extraction is "Skipped", or whose test status is "test success".
func shouldRunTest(tc testcases.TestCase) bool {
	es := strings.ToLower(tc.ExtractionStatus)
	ts := strings.ToLower(tc.TestStatus)
	if strings.Contains(es, "invalid") || strings.Contains(ts, "invalid") {
		return false
	}
	if strings.Contains(es, "skipped") {
		return false
	}
	if strings.Contains(ts, "test success") {
		return false
	}
	return true
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
