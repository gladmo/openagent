// Package tasks is a time-based automated-task scheduler for agents
// (openagent-native; not a pi port).
//
// It fires registered TaskSpecs on cron/every/at schedules and drives one
// full agent run per fire by injecting the task prompt into a target
// agent session through the Executor seam. The agent loop itself is never
// reentered or modified: the default AgentExecutor uses only
// Agent.PromptText, Agent.Abort, Agent.WaitForIdle, and Agent.IsStreaming.
//
// Semantics:
//
//   - Missed fires are skipped, never backfilled: after a restart,
//     resume, or registration, the next fire time is computed from now.
//   - A fire whose task still has a run in flight is recorded as skipped;
//     a global concurrency cap skips fires when exceeded.
//   - The TaskStore persists specs and recent run history atomically
//     (temp file + rename) and degrades per-line on corruption.
//   - Source tagging lives on Fire.Trigger and TaskRun records; the ai
//     message types stay untouched.
//
// The Scheduler here is time-driven and unrelated to the task phase
// state machine in agent/harness/pico3 (also called scheduler there).
package tasks
