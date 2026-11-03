package store

import (
	"blux/internal/core"
	"blux/internal/flowmodel"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *Store) SaveTemplate(ctx context.Context, t flowmodel.Template) error {
	if e := flowmodel.Validate(t); e != nil {
		return e
	}
	b, _ := json.Marshal(t)
	_, e := s.db.ExecContext(ctx, "INSERT INTO flow_templates(id,version,state_json) VALUES(?,?,?)", t.ID, t.Version, string(b))
	return e
}
func (s *Store) Templates(ctx context.Context) ([]flowmodel.Template, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT state_json FROM flow_templates ORDER BY id,version")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []flowmodel.Template{flowmodel.Default()}
	for rows.Next() {
		var b string
		var t flowmodel.Template
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(b), &t); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Delivery(ctx context.Context, id string) (d flowmodel.Delivery, e error) {
	var b string
	e = s.db.QueryRowContext(ctx, "SELECT state_json FROM deliveries WHERE id=?", id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		e = core.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal([]byte(b), &d)
	}
	return
}
func (s *Store) Deliveries(ctx context.Context) ([]flowmodel.Delivery, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT state_json FROM deliveries ORDER BY updated_at DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []flowmodel.Delivery{}
	for rows.Next() {
		var b string
		var d flowmodel.Delivery
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(b), &d); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) FlowReceipt(ctx context.Context, scope, key, hash string) (d flowmodel.Delivery, found bool, err error) {
	var h, b string
	err = s.db.QueryRowContext(ctx, "SELECT digest,result_json FROM flow_actions WHERE scope=? AND key=?", scope, key).Scan(&h, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return
	}
	if h != hash {
		return d, false, core.ErrConflict
	}
	err = json.Unmarshal([]byte(b), &d)
	return d, true, err
}
func (s *Store) SaveDelivery(ctx context.Context, d flowmodel.Delivery, expected int64, scope, key, hash string) (flowmodel.Delivery, error) {
	d.Revision = expected + 1
	b, _ := json.Marshal(d)
	err := s.transaction(ctx, func(q querier) error {
		if expected == 0 {
			if _, e := q.ExecContext(ctx, "INSERT INTO deliveries(id,state_json,revision,updated_at) VALUES(?,?,?,?)", d.ID, string(b), d.Revision, core.Now()); e != nil {
				return e
			}
		} else {
			res, e := q.ExecContext(ctx, "UPDATE deliveries SET state_json=?,revision=?,updated_at=? WHERE id=? AND revision=?", string(b), d.Revision, core.Now(), d.ID, expected)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return core.ErrConflict
			}
		}
		if key != "" {
			_, e := q.ExecContext(ctx, "INSERT INTO flow_actions(scope,key,digest,result_json) VALUES(?,?,?,?)", scope, key, hash, string(b))
			return e
		}
		return nil
	})
	return d, err
}
func (s *Store) TaskInputs(ctx context.Context, taskID string) ([]ArtifactVersion, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT a.id,a.snapshot_id,a.task_id,a.turn_id,a.path,a.hash,a.size,a.mime,a.created_at FROM artifact_versions a JOIN task_inputs i ON i.artifact_id=a.id WHERE i.task_id=? ORDER BY a.id", taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ArtifactVersion{}
	for rows.Next() {
		var a ArtifactVersion
		if e = rows.Scan(&a.ID, &a.SnapshotID, &a.TaskID, &a.TurnID, &a.Path, &a.Hash, &a.Size, &a.MIME, &a.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) WorkflowDelivery(ctx context.Context, taskID string) (id string, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT delivery_id FROM workflow_tasks WHERE task_id=?", taskID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}
