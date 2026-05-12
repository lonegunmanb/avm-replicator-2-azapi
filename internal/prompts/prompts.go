// Package prompts contains the prompt templates that the Go orchestrator
// sends to each Copilot session. They mirror the strings that the original
// PowerShell scripts (`migrate.ps1`, `run-coordinator.ps1`) and the in-prompt
// `copilot -p ...` templates inside `coordinator.md` used to produce.
//
// Important difference from the previous PowerShell flow: the orchestrator
// (Go) is now the *only* entity that spawns Copilot sessions. The agents'
// prompts therefore tell them to do their job and *exit* — they no longer ask
// the model to call `copilot -p ...` itself. Hand-off between roles still
// happens via on-disk state (`track.md`, `proof/*.md`).
package prompts

import "fmt"

// Planner prompts the planning phase (originally invoked by migrate.ps1).
func Planner(resourceType string) string {
	return fmt.Sprintf(
		"Read plan.md and play as planner role, follow its instructions and "+
			"prepare migration of %s defined in main.tf. When you are done, "+
			"exit. Do NOT spawn additional copilot sub-agents — the Go "+
			"orchestrator will run the next stage automatically.",
		resourceType,
	)
}

// Executor prompts an executor for a single root-level Argument task.
func ExecutorArgument(taskNo int, fieldName, resourceType string, isFirst bool) string {
	preamble := ""
	if isFirst {
		preamble = "As the first executor, create the initial Shadow Module " +
			"files (migrate_main.tf, migrate_variables.tf, migrate_outputs.tf, " +
			"migrate_validation.tf). "
	}
	return fmt.Sprintf(
		"You are an Executor Agent. Convert the root-level argument '%s' from "+
			"%s to azapi_resource. %s"+
			"You MUST strictly follow ALL rules in executor.md. If your approach "+
			"conflicts with executor.md, executor.md takes precedence. Do NOT "+
			"choose 'more conservative' or 'simpler' approaches — replicate "+
			"EXACT provider behavior or FAIL the task. Read track.md for "+
			"context and executor.md for instructions. Task #%d. When done, "+
			"update the task's Status column in track.md to 'Pending for check' "+
			"and exit. Do NOT call copilot CLI to spawn sub-agents — the Go "+
			"orchestrator will launch the checker.",
		fieldName, resourceType, preamble, taskNo,
	)
}

// ExecutorBlockSkeleton prompts an executor to create the SKELETON for a
// root-level nested block (the new Type-3 workflow).
func ExecutorBlockSkeleton(taskNo int, blockPath, resourceType string) string {
	return fmt.Sprintf(
		"You are an Executor Agent. Create the STRUCTURE SKELETON for "+
			"root-level block '%s' from %s to azapi_resource. DO NOT implement "+
			"individual arguments — only create the block framework with "+
			"comment placeholders for each field. Check for hidden fields in "+
			"this block's expand function. In your proof document, list all "+
			"child task numbers that are now ready for delegation. You MUST "+
			"strictly follow ALL rules in executor.md. Read track.md for "+
			"context. Task #%d. When done, update the task's Status to "+
			"'Pending for check' and exit. The Go orchestrator will launch "+
			"the checker and then schedule the child argument tasks.",
		blockPath, resourceType, taskNo,
	)
}

// ExecutorBlockArgument prompts an executor to fill in one argument inside an
// already-skeletonized block (Type-4).
func ExecutorBlockArgument(taskNo int, fullPath, resourceType string) string {
	return fmt.Sprintf(
		"You are an Executor Agent. Implement the block argument '%s' from %s "+
			"within the existing block skeleton. Find and replace the comment "+
			"placeholder for this field. The parent block structure already "+
			"exists. You MUST strictly follow ALL rules in executor.md. Read "+
			"track.md for context. Task #%d. When done, update the task's "+
			"Status to 'Pending for check' and exit.",
		fullPath, resourceType, taskNo,
	)
}

// ExecutorHiddenFields prompts the special HiddenFieldsCheck task.
func ExecutorHiddenFields(taskNo int, resourceType string) string {
	return fmt.Sprintf(
		"You are an Executor Agent. Check the provider's Create method for "+
			"hidden fields in the root properties block of %s. Add any "+
			"hardcoded/computed values (like orchestrationMode = 'Flexible') "+
			"to local.body.properties in migrate_main.tf. You MUST strictly "+
			"follow ALL rules in executor.md. Read track.md for context. "+
			"Task #%d. When done, update the task's Status to 'Pending for "+
			"check' and exit.",
		resourceType, taskNo,
	)
}

// Checker prompts the checker for a finished task.
func Checker(taskNo int, fieldName string) string {
	return fmt.Sprintf(
		"You are a Checker Agent in debug mode. Validate Task #%d (%s) "+
			"implementation. Read checker.md for your role and validation "+
			"rules. Read executor.md to understand what rules the executor "+
			"should have followed. Read the proof document %d.%s.md and the "+
			"implementation files (migrate_main.tf, migrate_variables.tf, "+
			"etc.). Verify the implementation exactly follows executor.md "+
			"rules. Either approve with signature OR fix issues and document "+
			"corrections in the proof document. When done, update the task's "+
			"Status in track.md (✅ Completed or Failed) and exit.",
		taskNo, fieldName, taskNo, fieldName,
	)
}

// PrepareTests prompts the acceptance-test preparation stage.
func PrepareTests(resourceType string) string {
	return fmt.Sprintf(
		"Read test_cases_planner.md and follow its instructions to prepare "+
			"acceptance tests for %s. When done, exit.",
		resourceType,
	)
}
