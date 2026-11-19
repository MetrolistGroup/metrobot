package db

// --- Reminders ---

type Reminder struct {
	ID        int64
	UserID    string
	ChannelID string
	DueAt     int64
	Content   string
}

func (d *DB) AddReminder(userID, channelID string, dueAt int64, content string) (int64, error) {
	res, err := d.conn.Exec(
		"INSERT INTO reminders (user_id, channel_id, due_at, content) VALUES (?, ?, ?, ?)",
		userID, channelID, dueAt, content,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) DeleteReminder(id int64) error {
	_, err := d.conn.Exec("DELETE FROM reminders WHERE id = ?", id)
	return err
}

func (d *DB) CountPendingReminders(userID string) (int, error) {
	var count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM reminders WHERE user_id = ?", userID).Scan(&count)
	return count, err
}

func (d *DB) GetPendingReminders() ([]Reminder, error) {
	rows, err := d.conn.Query("SELECT id, user_id, channel_id, due_at, content FROM reminders")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []Reminder
	for rows.Next() {
		var r Reminder
		if err := rows.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.DueAt, &r.Content); err != nil {
			return nil, err
		}
		reminders = append(reminders, r)
	}
	return reminders, rows.Err()
}
