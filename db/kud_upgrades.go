package db

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrKudMaxed = errors.New("upgrade is already maxed")

// KudUpgrade is one shop item. Level n+1 costs Base*(n+1) Kud, up to Max levels.
type KudUpgrade struct {
	Key, Title, Detail string
	Base, Max          int64
}

var KudUpgrades = []KudUpgrade{
	{Key: "swift", Title: "Swift hands", Detail: "Every Kud cooldown is 5% shorter per level.", Base: 2_000, Max: 6},
	{Key: "luck", Title: "Lucky charm", Detail: "+1% rescue chance per level in Mines and High-Low.", Base: 3_000, Max: 5},
}

func (d *DB) KudUpgradeLevels(userID string) (map[string]int64, error) {
	rows, err := d.conn.Query(`SELECT upgrade, level FROM kud_upgrades WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	levels := map[string]int64{}
	for rows.Next() {
		var key string
		var level int64
		if err := rows.Scan(&key, &level); err != nil {
			return nil, err
		}
		levels[key] = level
	}
	return levels, rows.Err()
}

// BuyKudUpgrade spends the next level's price and returns the new level and the cost paid.
func (d *DB) BuyKudUpgrade(userID, key string) (level, cost int64, err error) {
	var up KudUpgrade
	found := false
	for _, candidate := range KudUpgrades {
		if candidate.Key == key {
			up, found = candidate, true
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("unknown upgrade")
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var current int64
	err = tx.QueryRow(`SELECT level FROM kud_upgrades WHERE user_id=? AND upgrade=?`, userID, key).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	if current >= up.Max {
		return 0, 0, ErrKudMaxed
	}
	cost = up.Base * (current + 1)
	if err := spendKudTx(tx, userID, cost); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(`INSERT INTO kud_upgrades(user_id,upgrade,level) VALUES(?,?,1)
		ON CONFLICT(user_id,upgrade) DO UPDATE SET level=level+1 RETURNING level`, userID, key).Scan(&level); err != nil {
		return 0, 0, err
	}
	return level, cost, tx.Commit()
}
