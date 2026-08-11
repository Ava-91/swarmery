package wsingest

import (
	"errors"
	"reflect"
	"testing"
)

func TestParsePlanTable_Exported(t *testing.T) {
	readme := "# Plan\n\n" +
		"| # | Phase | Doc | Depends on |\n" +
		"|---|-------|-----|------------|\n" +
		"| 1 | Store | `phase-1-store.md` | — |\n" +
		"| 2 | API | `phase-2-api.md` | 1 (see decision D-11) |\n"

	phases, err := ParsePlanTable(readme)
	if err != nil {
		t.Fatalf("ParsePlanTable: %v", err)
	}
	want := []PlanPhase{
		{Seq: 1, Name: "Store", Doc: "phase-1-store.md"},
		{Seq: 2, Name: "API", Doc: "phase-2-api.md", DependsOn: []int{1, 11}},
	}
	if !reflect.DeepEqual(phases, want) {
		t.Errorf("phases = %+v, want %+v", phases, want)
	}
	// The RAW DependsOn deliberately keeps the stray 11 (no pruneDanglingDeps):
	// a strict validator must SEE the dangling number to reject it.
	if len(phases[1].DependsOn) != 2 {
		t.Errorf("DependsOn = %v, want the unpruned [1 11]", phases[1].DependsOn)
	}
}

func TestParsePlanTable_NoTable(t *testing.T) {
	if _, err := ParsePlanTable("# Plan\n\nJust prose, no table.\n"); !errors.Is(err, ErrNoPlanTable) {
		t.Fatalf("err = %v, want ErrNoPlanTable", err)
	}
}
