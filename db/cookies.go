package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CookieCooldown limits each giver to one cookie per rolling 24 hours.
const CookieCooldown = 24 * time.Hour

// GiveCookie moves one cookie from giver to receiver. ok is false while giver is cooling down; next is then the retry time.
func (d *DB) GiveCookie(giver, receiver string, now time.Time) (total int64, next time.Time, ok bool, err error) {
	if giver == "" || receiver == "" || giver == receiver {
		return 0, time.Time{}, false, fmt.Errorf("choose another person")
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return 0, time.Time{}, false, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`INSERT INTO cookies(user_id,last_given) VALUES(?,?)
		ON CONFLICT(user_id) DO UPDATE SET last_given=excluded.last_given WHERE cookies.last_given <= ?
		RETURNING last_given`, giver, now.Unix(), now.Add(-CookieCooldown).Unix()).Scan(new(int64))
	if errors.Is(err, sql.ErrNoRows) {
		var last int64
		if err := tx.QueryRow(`SELECT last_given FROM cookies WHERE user_id=?`, giver).Scan(&last); err != nil {
			return 0, time.Time{}, false, err
		}
		return 0, time.Unix(last, 0).Add(CookieCooldown), false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	if err := tx.QueryRow(`INSERT INTO cookies(user_id,received) VALUES(?,1)
		ON CONFLICT(user_id) DO UPDATE SET received=received+1 RETURNING received`, receiver).Scan(&total); err != nil {
		return 0, time.Time{}, false, err
	}
	return total, now.Add(CookieCooldown), true, tx.Commit()
}
