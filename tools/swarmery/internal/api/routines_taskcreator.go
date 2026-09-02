package api

// routinesTaskCreator is the api-layer adapter that satisfies
// routines.TaskCreator: a create-task step inserts a board task through the SAME
// path as POST /api/board/tasks (source='queue', minted external id, default
// column validation, task_updated WS publish, dispatcher poke), so the board
// stays the single source of truth for task semantics and the routines package
// never imports the api package (no cycle). Constructed in cmd/swarmery and
// handed to routines.NewService.

import (
	"database/sql"
	"fmt"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// RoutinesTaskCreator inserts board tasks on behalf of routine create-task steps.
// It holds only *sql.DB; the WS publish + dispatcher poke go through the same
// package-level hooks the board handlers use (publishTaskUpdated / pokeDispatch),
// which are no-ops when the bus/dispatcher are not attached.
type RoutinesTaskCreator struct {
	DB *sql.DB
}

// NewRoutinesTaskCreator builds the adapter.
func NewRoutinesTaskCreator(db *sql.DB) *RoutinesTaskCreator {
	return &RoutinesTaskCreator{DB: db}
}

// CreateTask inserts a board task (source='queue') in the given column for
// projectID and returns its external card id. Goes through the same
// constructor as POST /api/board/tasks (priority 'normal', empty
// file_scope/dependencies), so a routine step can no more mint a blank card
// than the board can: an empty title or prompt is an error, not a row. An
// unknown/blank column falls back to 'triage'.
func (c *RoutinesTaskCreator) CreateTask(projectID int64, title, prompt, column string) (string, error) {
	if column == "" || !validColumn(column) {
		column = "triage"
	}
	extID, err := newBoardExternalID()
	if err != nil {
		return "", err
	}
	id, _, err := store.InsertBoardTask(c.DB, store.BoardTaskInput{
		ProjectID:  projectID,
		Title:      title,
		Prompt:     prompt,
		Priority:   priorityLabels["normal"],
		Column:     column,
		ExternalID: extID,
	})
	if err != nil {
		return "", fmt.Errorf("insert board task: %w", err)
	}
	// Fan out the same signals a manual board POST would: notify WS subscribers
	// and poke the dispatcher (both no-ops when not attached).
	publishTaskUpdated(id)
	pokeDispatch()
	return extID, nil
}
