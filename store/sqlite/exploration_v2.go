package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/red-copilot/harness/exploration"
)

// ListKnowledge returns all durable knowledge for one task.
func (s *Store) ListKnowledge(ctx context.Context, taskID string) ([]exploration.Knowledge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, run_id, topic, kind, statement, scope, target, trust, reuse,
		       evidence_refs, sources, contradicts, valid_from, last_validated_at, expires_at, version
		FROM task_knowledge WHERE task_id=? ORDER BY valid_from, id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge: %w", err)
	}
	defer rows.Close()
	out := make([]exploration.Knowledge, 0)
	for rows.Next() {
		var item exploration.Knowledge
		var evidence, sources, contradicts []byte
		var validFrom, lastValidated, expiresAt sql.NullString
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.RunID, &item.Topic, &item.Kind, &item.Statement,
			&item.Scope, &item.Target, &item.Trust, &item.Reuse, &evidence, &sources,
			&contradicts, &validFrom, &lastValidated, &expiresAt, &item.Version,
		); err != nil {
			return nil, fmt.Errorf("scan knowledge: %w", err)
		}
		item.EvidenceRefs = decodeStringSlice(evidence)
		item.Sources = decodeStringSlice(sources)
		item.Contradicts = decodeStringSlice(contradicts)
		var parseErr error
		if item.ValidFrom, parseErr = parseOptionalTime(validFrom.String); parseErr != nil {
			return nil, parseErr
		}
		if item.LastValidated, parseErr = parseOptionalTime(lastValidated.String); parseErr != nil {
			return nil, parseErr
		}
		if item.ExpiresAt, parseErr = parseOptionalTime(expiresAt.String); parseErr != nil {
			return nil, parseErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SaveKnowledge inserts or updates one durable knowledge item.
func (s *Store) SaveKnowledge(ctx context.Context, item exploration.Knowledge) error {
	now := time.Now().UTC()
	if item.ValidFrom.IsZero() {
		item.ValidFrom = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO task_knowledge(
			id, task_id, run_id, topic, kind, statement, scope, target, trust, reuse,
			evidence_refs, sources, contradicts, valid_from, last_validated_at, expires_at,
			version, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			task_id=excluded.task_id, run_id=excluded.run_id, topic=excluded.topic,
			kind=excluded.kind, statement=excluded.statement, scope=excluded.scope,
			target=excluded.target, trust=excluded.trust, reuse=excluded.reuse,
			evidence_refs=excluded.evidence_refs, sources=excluded.sources,
			contradicts=excluded.contradicts, valid_from=excluded.valid_from,
			last_validated_at=excluded.last_validated_at, expires_at=excluded.expires_at,
			version=excluded.version, updated_at=excluded.updated_at`,
		item.ID, item.TaskID, item.RunID, item.Topic, item.Kind, item.Statement,
		item.Scope, item.Target, item.Trust, item.Reuse,
		encodeStringSlice(item.EvidenceRefs), encodeStringSlice(item.Sources), encodeStringSlice(item.Contradicts),
		formatOptionalTimeValue(item.ValidFrom), formatOptionalTimeValue(item.LastValidated), formatOptionalTimeValue(item.ExpiresAt),
		item.Version, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("save knowledge: %w", err)
	}
	return nil
}

// UpdateKnowledge updates one durable knowledge item by id.
func (s *Store) UpdateKnowledge(ctx context.Context, item exploration.Knowledge) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE task_knowledge SET run_id=?, topic=?, kind=?, statement=?, scope=?, target=?,
			trust=?, reuse=?, evidence_refs=?, sources=?, contradicts=?,
			valid_from=?, last_validated_at=?, expires_at=?, version=?, updated_at=?
		WHERE id=?`,
		item.RunID, item.Topic, item.Kind, item.Statement, item.Scope, item.Target,
		item.Trust, item.Reuse, encodeStringSlice(item.EvidenceRefs), encodeStringSlice(item.Sources),
		encodeStringSlice(item.Contradicts), formatOptionalTimeValue(item.ValidFrom),
		formatOptionalTimeValue(item.LastValidated), formatOptionalTimeValue(item.ExpiresAt),
		item.Version, formatTime(time.Now().UTC()), item.ID)
	if err != nil {
		return fmt.Errorf("update knowledge: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("update knowledge: %w", errNotFound)
	}
	return nil
}

var errNotFound = fmt.Errorf("not found")

func encodeStringSlice(values []string) []byte {
	if len(values) == 0 {
		return nil
	}
	data, _ := json.Marshal(values)
	return data
}

func decodeStringSlice(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var values []string
	_ = json.Unmarshal(data, &values)
	return values
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseTime(value)
}

func formatOptionalTimeValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

// SaveBranch inserts or updates one exploration branch.
func (s *Store) SaveBranch(ctx context.Context, branch exploration.Branch) error {
	now := time.Now().UTC()
	if branch.CreatedAt.IsZero() {
		branch.CreatedAt = now
	}
	if branch.UpdatedAt.IsZero() {
		branch.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO exploration_branches(
			id, parent_id, task_id, run_id, depth, topic, goal, hypothesis,
			success_condition, failure_condition, scope, budget, status, priority,
			knowledge_refs, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			parent_id=excluded.parent_id, task_id=excluded.task_id, run_id=excluded.run_id,
			depth=excluded.depth, topic=excluded.topic, goal=excluded.goal,
			hypothesis=excluded.hypothesis, success_condition=excluded.success_condition,
			failure_condition=excluded.failure_condition, scope=excluded.scope,
			budget=excluded.budget, status=excluded.status, priority=excluded.priority,
			knowledge_refs=excluded.knowledge_refs, updated_at=excluded.updated_at`,
		branch.ID, branch.ParentID, branch.TaskID, branch.RunID, branch.Depth, branch.Topic,
		branch.Goal, branch.Hypothesis, branch.SuccessCondition, branch.FailureCondition,
		branch.Scope, encodeJSON(branch.Budget), branch.Status, branch.Priority,
		encodeJSON(branch.KnowledgeRefs), formatTime(branch.CreatedAt), formatTime(branch.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save branch: %w", err)
	}
	return nil
}

// SaveAttempt inserts or updates one branch attempt.
func (s *Store) SaveAttempt(ctx context.Context, attempt exploration.Attempt) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO exploration_attempts(
			id, branch_id, run_id, session_id, status, outcome, observation_ids,
			started_at, finished_at, error)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status, outcome=excluded.outcome,
			observation_ids=excluded.observation_ids, finished_at=excluded.finished_at,
			error=excluded.error`,
		attempt.ID, attempt.BranchID, attempt.RunID, attempt.SessionID,
		attempt.Status, attempt.Outcome, encodeJSON(attempt.ObservationIDs),
		formatOptionalTimeValue(attempt.StartedAt), formatOptionalTimeValue(attempt.FinishedAt), attempt.Error)
	if err != nil {
		return fmt.Errorf("save attempt: %w", err)
	}
	return nil
}

// SaveObservation inserts one immutable observation.
func (s *Store) SaveObservation(ctx context.Context, observation exploration.Observation) error {
	createdAt := observation.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO exploration_observations(
			id, branch_id, attempt_id, kind, action, target, summary, data, evidence_refs, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		observation.ID, observation.BranchID, observation.AttemptID, observation.Kind,
		observation.Action, observation.Target, observation.Summary, observation.Data,
		encodeJSON(observation.EvidenceRefs), formatTime(createdAt))
	if err != nil {
		return fmt.Errorf("save observation: %w", err)
	}
	return nil
}

func encodeJSON(value any) []byte {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	return data
}

// ListBranches returns branches for one run ordered by depth and creation.
func (s *Store) ListBranches(ctx context.Context, runID string) ([]exploration.Branch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, parent_id, task_id, run_id, depth, topic, goal, hypothesis,
		       success_condition, failure_condition, scope, budget, status, priority,
		       knowledge_refs, created_at, updated_at
		FROM exploration_branches WHERE run_id=? ORDER BY depth, created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	defer rows.Close()
	out := make([]exploration.Branch, 0)
	for rows.Next() {
		var branch exploration.Branch
		var budget, refs []byte
		var createdAt, updatedAt string
		if err := rows.Scan(
			&branch.ID, &branch.ParentID, &branch.TaskID, &branch.RunID, &branch.Depth,
			&branch.Topic, &branch.Goal, &branch.Hypothesis, &branch.SuccessCondition,
			&branch.FailureCondition, &branch.Scope, &budget, &branch.Status, &branch.Priority,
			&refs, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan branch: %w", err)
		}
		_ = json.Unmarshal(budget, &branch.Budget)
		_ = json.Unmarshal(refs, &branch.KnowledgeRefs)
		if branch.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		if branch.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return nil, err
		}
		out = append(out, branch)
	}
	return out, rows.Err()
}
