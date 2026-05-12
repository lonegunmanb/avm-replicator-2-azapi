// Package track parses and updates the replicator's `track.md` file.
//
// `track.md` is a Markdown table that drives the migration. Rows look like:
//
//	| 1 | name                    | Argument | Yes | Pending           |               |
//	| 2 | resource_group_name     | Argument | Yes | ✅ Completed       | [2.rgn.md]() |
//	| 3 | os_disk                 | Block    | Yes | Pending for check |               |
//
// The orchestrator only reads (it relies on each agent to *write* its own row's
// status), but it needs to enumerate tasks and inspect their current status to
// decide what to do next.
package track

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Status is one of the known status values used in track.md.
type Status string

const (
	StatusPending         Status = "Pending"
	StatusInProgress      Status = "In Progress"
	StatusPendingForCheck Status = "Pending for check"
	StatusCompleted       Status = "✅ Completed"
	StatusFailed          Status = "Failed"
	StatusUnknown         Status = ""
)

// Task is one row of `track.md`.
type Task struct {
	No       int
	Path     string
	Type     string // "Argument" | "Block" | other
	Required string
	Status   Status
	ProofDoc string
}

// IsRoot reports whether the task is a root-level item (path has no dots).
func (t Task) IsRoot() bool { return !strings.Contains(t.Path, ".") }

// IsBlock reports whether the task represents a (nested) block.
func (t Task) IsBlock() bool { return strings.EqualFold(t.Type, "Block") }

// IsArgument reports whether the task represents an argument.
func (t Task) IsArgument() bool { return strings.EqualFold(t.Type, "Argument") }

// rowRegexp matches a track.md task row. We require the leading number column
// so we don't accidentally pick up the table header / separator rows.
var rowRegexp = regexp.MustCompile(`^\|\s*(\d+)\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*([^|]*?)\s*\|\s*([^|]*?)\s*\|\s*$`)

// Parse reads track.md from `path` and returns its task list in file order.
func Parse(path string) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open track.md: %w", err)
	}
	defer f.Close()

	var tasks []Task
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		m := rowRegexp.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		no, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		tasks = append(tasks, Task{
			No:       no,
			Path:     m[2],
			Type:     m[3],
			Required: m[4],
			Status:   normalizeStatus(m[5]),
			ProofDoc: m[6],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read track.md: %w", err)
	}
	return tasks, nil
}

func normalizeStatus(raw string) Status {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return StatusPending
	case strings.Contains(s, "Completed"):
		return StatusCompleted
	case strings.EqualFold(s, "In Progress"):
		return StatusInProgress
	case strings.EqualFold(s, "Pending for check"):
		return StatusPendingForCheck
	case strings.EqualFold(s, "Failed"):
		return StatusFailed
	case strings.EqualFold(s, "Pending"):
		return StatusPending
	default:
		return Status(s)
	}
}

// NextActionable returns the first task whose status indicates work is still
// to be done (Pending, In Progress, Pending for check, or Failed). Returns
// (zero, false) when every task is completed.
func NextActionable(tasks []Task) (Task, bool) {
	for _, t := range tasks {
		switch t.Status {
		case StatusCompleted:
			continue
		default:
			return t, true
		}
	}
	return Task{}, false
}

// Counts is a small summary used by the TUI.
type Counts struct {
	Total          int
	Completed      int
	InProgress     int
	PendingForChk  int
	Failed         int
	Pending        int
}

// Summarize aggregates the task list into bucket counts.
func Summarize(tasks []Task) Counts {
	var c Counts
	c.Total = len(tasks)
	for _, t := range tasks {
		switch t.Status {
		case StatusCompleted:
			c.Completed++
		case StatusInProgress:
			c.InProgress++
		case StatusPendingForCheck:
			c.PendingForChk++
		case StatusFailed:
			c.Failed++
		default:
			c.Pending++
		}
	}
	return c
}
