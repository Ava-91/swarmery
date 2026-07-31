package planning

import "testing"

// TestProcessAlive_FindsARestartSurvivor: the planner spawn is process-group
// isolated, so it outlives a daemon restart. After a restart both in-memory
// sources are empty, and without the process-table probe the wizard would read a
// planner that is still writing as dead and roll it back under the operator.
func TestProcessAlive_FindsARestartSurvivor(t *testing.T) {
	s := NewService(nil, nil) // no DB needed: processAlive touches none
	var probed string
	s.FindRun = func(uuid string) (int, bool) { probed = uuid; return 4242, uuid == "live-uuid" }

	if !s.processAlive(1, "live-uuid") {
		t.Error("a planner found in the process table must read as alive")
	}
	if probed != "live-uuid" {
		t.Errorf("probed uuid = %q, want live-uuid", probed)
	}
	if s.processAlive(1, "dead-uuid") {
		t.Error("a uuid with no process must read as dead")
	}
}

// TestProcessAlive_InMemorySourcesShortCircuit: the ps scan is the last resort —
// a planner this process owns must be answered without shelling out.
func TestProcessAlive_InMemorySourcesShortCircuit(t *testing.T) {
	s := NewService(nil, nil)
	s.FindRun = func(string) (int, bool) { t.Error("must not scan when an in-memory source answers"); return 0, false }

	s.mu.Lock()
	s.active[7] = run{uuid: "slot-uuid"}
	s.mu.Unlock()
	if !s.processAlive(7, "slot-uuid") {
		t.Error("the planner slot must report alive")
	}

	s.ResumeInFlight = func(string) bool { return true }
	if !s.processAlive(9, "resume-uuid") {
		t.Error("an in-flight resume must report alive")
	}
}
