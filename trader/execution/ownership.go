package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// terminalManagedRunStates lists the managed run states that no longer reserve
// the account. "expired" is provably terminal: planning/live retires the
// approval before any order exists and deletes its lease row.
var terminalManagedRunStates = []string{"completed", "cancelled", "failed", "expired"}

// managedRunRef is the minimal projection of planning/live.Run.
// trader/execution cannot import planning/live because it imports this package.
type managedRunRef struct {
	ID         string `gorm:"primaryKey"`
	UserID     string `gorm:"column:user_id"`
	ExchangeID string `gorm:"column:exchange_id"`
	State      string `gorm:"column:state"`
}

func (managedRunRef) TableName() string { return "planning_live_runs" }

// RequireLegacyAllowed refuses the legacy autotrader when an exchange
// configuration belongs to managed execution: it has an enabled physical
// account binding, or a managed run that has not reached a terminal state.
// ruling: an existing but disabled binding (the Binance case, which has no
// signed UID) does not block legacy trading; only an enabled one does.
func RequireLegacyAllowed(ctx context.Context, db *gorm.DB, user, exchangeID string) error {
	if db == nil || user == "" {
		return errors.New("cannot verify managed account ownership")
	}
	if exchangeID == "" {
		return nil
	}
	bindings, runs, err := ownershipTables(db)
	if err != nil {
		return fmt.Errorf("cannot verify managed account ownership: %w", err)
	}
	if bindings {
		var b Binding
		switch err := db.WithContext(ctx).Where("user_id = ? AND exchange_id = ?", user, exchangeID).First(&b).Error; {
		case errors.Is(err, gorm.ErrRecordNotFound):
		case err != nil:
			return fmt.Errorf("cannot verify managed account ownership: %w", err)
		case b.Enabled:
			return errors.New("this exchange account is bound to unattended execution; the legacy autotrader cannot trade it")
		}
	}
	if runs {
		var held []managedRunRef
		err := db.WithContext(ctx).
			Where("user_id = ? AND exchange_id = ? AND state NOT IN ?", user, exchangeID, terminalManagedRunStates).
			Limit(1).Find(&held).Error
		if err != nil {
			return fmt.Errorf("cannot verify managed account ownership: %w", err)
		}
		if len(held) != 0 {
			return fmt.Errorf("this exchange account has a managed trade in state %q; the legacy autotrader cannot trade it", held[0].State)
		}
	}
	return nil
}

// RequireLegacyIdentity refuses the legacy autotrader when this configuration's
// credentials resolve to the same physical venue account that an enabled
// binding already manages under another NOFX configuration. Any identity read
// failure is treated as a refusal.
// ruling: only venues that implement AccountIdentity are covered; Binance
// cannot prove a UID, so its binding alone carries the dedicated-account
// assumption.
func RequireLegacyIdentity(ctx context.Context, db *gorm.DB, exchangeID, venue string, identity AccountIdentity) error {
	if identity == nil || exchangeID == "" || venue == "" {
		return nil
	}
	if db == nil {
		return errors.New("cannot verify managed account ownership")
	}
	bindings, _, err := ownershipTables(db)
	if err != nil {
		return fmt.Errorf("cannot verify managed account ownership: %w", err)
	}
	if !bindings {
		return nil
	}
	var bound []Binding
	if err := db.WithContext(ctx).Where("venue = ? AND enabled = ?", venue, true).Find(&bound).Error; err != nil {
		return fmt.Errorf("cannot verify managed account ownership: %w", err)
	}
	if len(bound) == 0 {
		return nil
	}
	uid, err := identity.VenueAccountID(ctx)
	if err != nil {
		return fmt.Errorf("cannot read the venue account identity: %w", err)
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return errors.New("cannot read the venue account identity")
	}
	for _, b := range bound {
		if b.VenueAccountID == uid && b.ExchangeID != exchangeID {
			return errors.New("this physical account is managed under another NOFX exchange configuration; the legacy autotrader cannot trade it")
		}
	}
	return nil
}

// ownershipTables reports which managed tables exist. HasTable() folds a broken
// connection into "missing", which would let a database read failure pass for
// "not managed", so probe the database first.
func ownershipTables(db *gorm.DB) (bindings, runs bool, err error) {
	var probe int
	if err = db.Raw("SELECT 1").Scan(&probe).Error; err != nil {
		return false, false, err
	}
	return db.Migrator().HasTable(&Binding{}), db.Migrator().HasTable(&managedRunRef{}), nil
}
