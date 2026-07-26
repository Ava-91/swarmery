package trajjudge

import (
	"fmt"
	"strings"
)

// event is one row of the events table reduced to what the summary needs.
type event struct {
	seq  int64
	typ  string
	tool string
}

// summarizeTrajectory renders an ordered, compact call-tree dump. Each line is
// "t<seq> <type> <tool>", so the judge can cite steps as [t<seq>] in evidence.
func summarizeTrajectory(evs []event) string {
	var b strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&b, "t%d %s", e.seq, e.typ)
		if e.tool != "" {
			fmt.Fprintf(&b, " %s", e.tool)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// buildRubricPrompt asks for a strict-JSON verdict on the 4-dimension rubric.
func buildRubricPrompt(summary string) string {
	return `You are an expert reviewer scoring one AI coding agent's execution trajectory.
Score each dimension from 1 (worst) to 5 (best). Higher is always better.

- end_result: did the run reach a correct, complete outcome?
- instruction_compliance: did it follow the task's instructions and scope?
- pitfalls: 5 = no anti-patterns; 1 = many (search loops, skipped verification, thrash).
- tool_calls: were tool choices appropriate and economical?

Cite specific steps as [t<seq>] in the review. Respond with ONLY a JSON object:
{"end_result":N,"instruction_compliance":N,"pitfalls":N,"tool_calls":N,"review":"<2-4 sentences with [t<seq>] evidence>"}

Trajectory (one step per line, "t<seq> <type> <tool>"):
` + summary
}
