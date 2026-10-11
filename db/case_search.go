package db

import (
	"context"
	"strconv"
	"time"
)

// CaseSearch uses an exclusive upper timestamp and case-number cursor.
type CaseSearch struct {
	From, Until time.Time
	FromSet     bool // Distinguishes the valid date 0001-01-01 from an omitted bound.
	Text        string
	Before      int64
}

// SearchCases reads all platforms without a total-history cap, 50 cases at a time.
func (d *DB) SearchCases(ctx context.Context, filter CaseSearch) ([]Case, string, error) {
	query := `SELECT id, case_number, action_type, platform, target_id, moderator_id, COALESCE(reason, ''), timestamp FROM cases WHERE 1=1`
	args := []any{}
	if filter.FromSet || !filter.From.IsZero() {
		query += ` AND timestamp >= ?`
		args = append(args, filter.From.Unix())
	}
	if !filter.Until.IsZero() {
		query += ` AND timestamp < ?`
		args = append(args, filter.Until.Unix())
	}
	if filter.Before != 0 {
		query += ` AND case_number < ?`
		args = append(args, filter.Before)
	}
	if filter.Text != "" {
		// instr treats SQL wildcard characters as literal text.
		query += ` AND (instr(CAST(case_number AS TEXT), ?) > 0 OR instr(lower(platform), lower(?)) > 0 OR instr(lower(action_type), lower(?)) > 0 OR instr(lower(target_id), lower(?)) > 0 OR instr(lower(moderator_id), lower(?)) > 0 OR instr(lower(COALESCE(reason, '')), lower(?)) > 0)`
		for range 6 {
			args = append(args, filter.Text)
		}
	}
	query += ` ORDER BY case_number DESC LIMIT 51`
	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Case{}
	for rows.Next() {
		var c Case
		if err := rows.Scan(&c.ID, &c.CaseNumber, &c.ActionType, &c.Platform, &c.TargetID, &c.ModeratorID, &c.Reason, &c.Timestamp); err != nil {
			return nil, "", err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = strconv.FormatInt(items[49].CaseNumber, 10)
	}
	return items, next, nil
}
