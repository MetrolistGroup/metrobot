package db

import (
	"database/sql"
	"errors"
	"time"
)

type KudosEntry struct {
	UserID  string
	Balance int64
}

// DailyCooldown mirrors Sigma's 22-hour daily window.
const DailyCooldown = 79200 * time.Second

// DailyAmount is Sigma's daily reward: base 5000 + randomPart (0..999), a streak bonus of 1600 per streak
// capped at a streak of 10, plus the streak itself. A streak of 1 gets no bonus.
func DailyAmount(streak, randomPart int64) int64 {
	multi := streak
	if multi > 10 {
		multi = 10
	}
	amount := int64(5000) + randomPart
	if multi != 1 {
		amount += 1600 * multi
	}
	return amount + streak
}

// ClaimDaily bumps the streak and credits DailyAmount once per DailyCooldown, atomically.
// While on cooldown it returns the current streak and the next claim time with ok=false.
func (d *DB) ClaimDaily(userID string, randomPart int64, now time.Time) (amount, streak, balance int64, next time.Time, ok bool, err error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return 0, 0, 0, time.Time{}, false, err
	}
	defer tx.Rollback()
	var last int64
	err = tx.QueryRow(`SELECT balance, last_daily, streak FROM kudos WHERE user_id = ?`, userID).Scan(&balance, &last, &streak)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, time.Time{}, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) && now.Unix() <= last+int64(DailyCooldown.Seconds()) {
		return 0, streak, balance, time.Unix(last, 0).Add(DailyCooldown), false, nil
	}
	streak++
	amount = DailyAmount(streak, randomPart)
	if err = tx.QueryRow(`INSERT INTO kudos (user_id, balance, last_daily, streak) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET balance = balance + excluded.balance, last_daily = excluded.last_daily, streak = excluded.streak
		RETURNING balance`, userID, amount, now.Unix(), streak).Scan(&balance); err != nil {
		return 0, 0, 0, time.Time{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, 0, time.Time{}, false, err
	}
	return amount, streak, balance, now.Add(DailyCooldown), true, nil
}

func (d *DB) Kudos(userID string) (int64, error) {
	var balance int64
	err := d.conn.QueryRow(`SELECT balance FROM kudos WHERE user_id = ?`, userID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return balance, err
}

// SpendKudos deducts amount atomically; ok is false when the balance is too low.
func (d *DB) SpendKudos(userID string, amount int64) (balance int64, ok bool, err error) {
	err = d.conn.QueryRow(`UPDATE kudos SET balance = balance - ? WHERE user_id = ? AND balance >= ? RETURNING balance`, amount, userID, amount).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return balance, err == nil, err
}

func (d *DB) AddKudos(userID string, amount int64) (int64, error) {
	var balance int64
	err := d.conn.QueryRow(`INSERT INTO kudos (user_id, balance) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET balance = balance + excluded.balance
		RETURNING balance`, userID, amount).Scan(&balance)
	return balance, err
}

func (d *DB) KudosLeaderboard(limit int) ([]KudosEntry, error) {
	rows, err := d.conn.Query(`SELECT user_id, balance FROM kudos WHERE balance > 0 ORDER BY balance DESC, user_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []KudosEntry
	for rows.Next() {
		var entry KudosEntry
		if err := rows.Scan(&entry.UserID, &entry.Balance); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// KudosRank returns the user's 1-based rank among positive balances, or 0 when they have none.
func (d *DB) KudosRank(userID string) (int, error) {
	var rank int
	err := d.conn.QueryRow(`SELECT (SELECT COUNT(*) FROM kudos o WHERE o.balance > k.balance) + 1 FROM kudos k WHERE k.user_id = ? AND k.balance > 0`, userID).Scan(&rank)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return rank, err
}

type StarEntry struct {
	UserID string
	Stars  int64
	Posts  int64
}

// StarLeaderboard ranks authors by total stars across the starboard and sobboard combined.
func (d *DB) StarLeaderboard(limit int) ([]StarEntry, error) {
	rows, err := d.conn.Query(`SELECT author_id, SUM(star_count) AS stars, COUNT(*) AS posts FROM (
			SELECT author_id, star_count FROM starboard
			UNION ALL
			SELECT author_id, star_count FROM sobboard
		) GROUP BY author_id HAVING stars > 0 ORDER BY stars DESC, posts DESC, author_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []StarEntry
	for rows.Next() {
		var entry StarEntry
		if err := rows.Scan(&entry.UserID, &entry.Stars, &entry.Posts); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
