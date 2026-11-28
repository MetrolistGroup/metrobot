package db

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrKudMaxed = errors.New("upgrade is already maxed")
var ErrKudQuoteChanged = errors.New("upgrade level or price changed")

// KudUpgrade is one shop item. Level n costs Base*n*n Kud, up to Max levels.
type KudUpgrade struct {
	Key, Title, Detail string
	Base, Max          int64
}

var KudUpgrades = []KudUpgrade{
	{Key: "swift", Title: "Swift hands", Detail: "Cooldowns are 0.2% shorter per level, up to 10%.", Base: 10_000, Max: 50},
	{Key: "luck", Title: "Lucky charm", Detail: "+0.04% rescue chance per level in Mines and High-Low, up to +2%.", Base: 15_000, Max: 50},
}

func (up KudUpgrade) Cost(level int64) int64 { return up.Base * level * level }

func KudUpgradeByKey(key string) (KudUpgrade, bool) {
	for _, up := range KudUpgrades {
		if up.Key == key {
			return up, true
		}
	}
	return KudUpgrade{}, false
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

// BuyKudUpgrade charges only the confirmed level and price, so repeated confirmations cannot buy extra levels.
func (d *DB) BuyKudUpgrade(userID, key string, expectedLevel, expectedCost int64) (level, cost int64, err error) {
	up, found := KudUpgradeByKey(key)
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
	cost = up.Cost(current + 1)
	if expectedLevel != current+1 || expectedCost != cost {
		return 0, 0, ErrKudQuoteChanged
	}
	if err := spendKudTx(tx, userID, cost); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(`INSERT INTO kud_upgrades(user_id,upgrade,level) VALUES(?,?,1)
		ON CONFLICT(user_id,upgrade) DO UPDATE SET level=level+1 RETURNING level`, userID, key).Scan(&level); err != nil {
		return 0, 0, err
	}
	return level, cost, tx.Commit()
}
