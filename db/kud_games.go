package db

import (
	"database/sql"
	"errors"
	"fmt"
)

const MaxKudBet int64 = 100_000_000
const maxKudBalance int64 = 9_000_000_000_000_000

var ErrKudFunds = errors.New("not enough Kud")
var ErrKudClosed = errors.New("this game has already ended")

type KudAccount struct {
	Balance, Played, Wins, Draws, Wagered, Paid int64
	Debt, Principal, Lent, Held                 int64
	Lender                                      string
}

func (d *DB) KudAccount(userID string) (a KudAccount, err error) {
	err = d.conn.QueryRow(`SELECT
		COALESCE((SELECT balance FROM kudos WHERE user_id=?),0),
		COALESCE(s.played,0), COALESCE(s.wins,0), COALESCE(s.draws,0), COALESCE(s.wagered,0), COALESCE(s.paid,0),
		COALESCE(l.remaining,0), COALESCE(l.principal,0), COALESCE(l.lender,''),
		(SELECT COALESCE(SUM(remaining),0) FROM kud_loans WHERE lender=?),
		(SELECT COALESCE(SUM(stake),0) FROM kud_escrow WHERE user_id=?)
		FROM (SELECT ? AS id) u LEFT JOIN kud_stats s ON s.user_id=u.id LEFT JOIN kud_loans l ON l.borrower=u.id`,
		userID, userID, userID, userID).Scan(&a.Balance, &a.Played, &a.Wins, &a.Draws, &a.Wagered, &a.Paid, &a.Debt, &a.Principal, &a.Lender, &a.Lent, &a.Held)
	return
}

func addKudTx(tx *sql.Tx, userID string, amount int64) (int64, error) {
	if userID == "" || amount < 0 || amount > maxKudBalance {
		return 0, fmt.Errorf("invalid Kud credit")
	}
	var balance int64
	err := tx.QueryRow(`INSERT INTO kudos(user_id,balance) VALUES(?,?)
		ON CONFLICT(user_id) DO UPDATE SET balance=balance+excluded.balance WHERE balance <= ?-excluded.balance
		RETURNING balance`, userID, amount, maxKudBalance).Scan(&balance)
	return balance, err
}

// Repayment transfers do not trigger another tax, so circular loans cannot recurse.
func creditKudTx(tx *sql.Tx, userID string, amount int64) (int64, error) {
	if amount < 0 || amount > maxKudBalance {
		return 0, fmt.Errorf("invalid Kud payout")
	}
	var lender string
	var remaining, carry int64
	err := tx.QueryRow(`SELECT lender,remaining,carry FROM kud_loans WHERE borrower=?`, userID).Scan(&lender, &remaining, &carry)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil && amount > 0 {
		tax := min(remaining, (amount+carry)/5)
		if _, err := addKudTx(tx, lender, tax); err != nil {
			return 0, err
		}
		if tax == remaining {
			_, err = tx.Exec(`DELETE FROM kud_loans WHERE borrower=?`, userID)
		} else {
			_, err = tx.Exec(`UPDATE kud_loans SET remaining=remaining-?,carry=? WHERE borrower=?`, tax, (amount+carry)%5, userID)
		}
		if err != nil {
			return 0, err
		}
		amount -= tax
	}
	return addKudTx(tx, userID, amount)
}

func spendKudTx(tx *sql.Tx, userID string, amount int64) error {
	if amount <= 0 || amount > MaxKudBet {
		return fmt.Errorf("stake must be between 1 and %d Kud", MaxKudBet)
	}
	result, err := tx.Exec(`UPDATE kudos SET balance=balance-? WHERE user_id=? AND balance>=?`, amount, userID, amount)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrKudFunds
	}
	return err
}

func (d *DB) HoldKud(gameID, game, userID string, stake int64) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := spendKudTx(tx, userID, stake); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO kud_escrow(game_id,game,user_id,stake) VALUES(?,?,?,?)`, gameID, game, userID, stake); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) DoubleKudHold(gameID, userID string) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stake int64
	if err := tx.QueryRow(`SELECT stake FROM kud_escrow WHERE game_id=? AND user_id=?`, gameID, userID).Scan(&stake); err != nil {
		return err
	}
	if stake > MaxKudBet/2 {
		return fmt.Errorf("double down exceeds the stake limit")
	}
	if err := spendKudTx(tx, userID, stake); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE kud_escrow SET stake=stake*2 WHERE game_id=? AND user_id=?`, gameID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SettleKud pays every entrant, records results, and consumes escrow in one transaction.
func (d *DB) SettleKud(gameID string, payouts map[string]int64) (map[string]int64, error) {
	return d.finishKud(gameID, payouts, false)
}

func (d *DB) RefundKud(gameID string) error {
	_, err := d.finishKud(gameID, nil, true)
	return err
}

func (d *DB) finishKud(gameID string, payouts map[string]int64, refund bool) (map[string]int64, error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT user_id,stake FROM kud_escrow WHERE game_id=?`, gameID)
	if err != nil {
		return nil, err
	}
	stakes := map[string]int64{}
	for rows.Next() {
		var userID string
		var stake int64
		if err := rows.Scan(&userID, &stake); err != nil {
			rows.Close()
			return nil, err
		}
		stakes[userID] = stake
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(stakes) == 0 {
		return nil, ErrKudClosed
	}
	if !refund && len(stakes) != len(payouts) {
		return nil, fmt.Errorf("payouts must cover every entrant")
	}
	balances := map[string]int64{}
	for userID, stake := range stakes {
		if refund {
			balances[userID], err = addKudTx(tx, userID, stake)
		} else {
			payout, ok := payouts[userID]
			if !ok || payout < 0 || payout > MaxKudBet*100_000 {
				return nil, fmt.Errorf("invalid game payout")
			}
			balances[userID], err = creditKudTx(tx, userID, payout)
			if err == nil {
				_, err = tx.Exec(`INSERT INTO kud_stats(user_id,played,wins,draws,wagered,paid) VALUES(?,1,?,?,?,?)
					ON CONFLICT(user_id) DO UPDATE SET played=played+1,wins=wins+excluded.wins,draws=draws+excluded.draws,
					wagered=wagered+excluded.wagered,paid=paid+excluded.paid`, userID, payout > stake, payout == stake, stake, payout)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM kud_escrow WHERE game_id=?`, gameID); err != nil {
		return nil, err
	}
	return balances, tx.Commit()
}

// RecoverKud refunds unfinished games before accepting commands after a restart.
func (d *DB) RecoverKud() error {
	rows, err := d.conn.Query(`SELECT DISTINCT game_id FROM kud_escrow`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := d.RefundKud(id); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) FundKudLoan(lender, borrower string, amount int64) error {
	if lender == "" || borrower == "" || lender == borrower {
		return fmt.Errorf("choose another person as lender")
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM kud_loans WHERE borrower=?`, borrower).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("repay the current loan first")
	}
	if err := spendKudTx(tx, lender, amount); err != nil {
		return err
	}
	if _, err := addKudTx(tx, borrower, amount); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO kud_loans(borrower,lender,principal,remaining) VALUES(?,?,?,?)`, borrower, lender, amount, amount); err != nil {
		return err
	}
	return tx.Commit()
}
