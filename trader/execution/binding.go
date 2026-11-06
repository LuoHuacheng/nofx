package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AccountIdentity is implemented by adapters that can read the immutable
// physical account ID from a signed, read-only venue query. Adapters that
// cannot prove it must not implement this interface.
type AccountIdentity interface {
	VenueAccountID(ctx context.Context) (string, error)
}

// Binding ties one NOFX exchange configuration to the physical venue account.
// It stores no credentials: the stored API key hash is not a physical identity.
type Binding struct {
	ID             string      `gorm:"primaryKey" json:"id"`
	UserID         string      `gorm:"column:user_id;not null;index" json:"user_id"`
	ExchangeID     string      `gorm:"column:exchange_id;not null;index" json:"exchange_id"`
	Venue          string      `gorm:"column:venue;not null;uniqueIndex:managed_account_identity,priority:1" json:"venue"`
	Environment    Environment `gorm:"column:environment;not null;uniqueIndex:managed_account_identity,priority:2" json:"environment"`
	VenueAccountID string      `gorm:"column:venue_account_id;not null;uniqueIndex:managed_account_identity,priority:3" json:"venue_account_id"`
	VerifiedAt     time.Time   `gorm:"column:verified_at" json:"verified_at"`
	Enabled        bool        `gorm:"column:enabled;not null;default:false" json:"enabled"`
}

func (Binding) TableName() string { return "managed_account_bindings" }

// ResolveBinding returns the binding of one credential configuration.
func ResolveBinding(ctx context.Context, db *gorm.DB, user, exchangeID string) (Binding, error) {
	if db == nil || user == "" || exchangeID == "" {
		return Binding{}, errors.New("managed binding requires a user and an exchange configuration")
	}
	var b Binding
	err := db.WithContext(ctx).Where("user_id = ? AND exchange_id = ?", user, exchangeID).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Binding{}, errors.New("this exchange configuration is not bound to a physical account")
	}
	if err != nil {
		return Binding{}, fmt.Errorf("read the managed account binding: %w", err)
	}
	return b, nil
}

// BindManagedAccount creates or rotates the binding of one credential
// configuration. One physical account keeps exactly one binding and one
// credential configuration; an unverified identity is never enabled.
func BindManagedAccount(ctx context.Context, db *gorm.DB, b Binding) (Binding, error) {
	if db == nil || b.UserID == "" || b.ExchangeID == "" || b.Venue == "" || b.VenueAccountID == "" {
		return Binding{}, errors.New("managed binding requires a user, an exchange configuration, a venue and a venue account ID")
	}
	if !b.Environment.Valid() {
		return Binding{}, errors.New("managed binding environment must be sandbox or live")
	}
	if b.VerifiedAt.IsZero() {
		b.Enabled = false
	}
	var out Binding
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var known Binding
		err := tx.Where("venue = ? AND environment = ? AND venue_account_id = ?", b.Venue, b.Environment, b.VenueAccountID).First(&known).Error
		switch {
		case err == nil:
			if known.UserID != b.UserID {
				return errors.New("this physical account is already bound to another NOFX user")
			}
			if known.ExchangeID != b.ExchangeID {
				return errors.New("this physical account is already bound to another credential configuration")
			}
			known.VerifiedAt, known.Enabled = b.VerifiedAt, b.Enabled
			if err := tx.Model(&known).Updates(map[string]any{"verified_at": known.VerifiedAt, "enabled": known.Enabled}).Error; err != nil {
				return err
			}
			out = known
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		var occupied Binding
		err = tx.Where("user_id = ? AND exchange_id = ?", b.UserID, b.ExchangeID).First(&occupied).Error
		if err == nil {
			return errors.New("this credential configuration is already bound to another physical account")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		b.ID = uuid.NewString()
		out = b
		return tx.Create(&b).Error
	})
	if err != nil {
		return Binding{}, err
	}
	return out, nil
}

type boundTrader struct {
	ManagedTrader
	binding Binding
}

// WithBinding tags snapshots and events with the durable binding ID so adapters
// never infer a database identity.
func WithBinding(t ManagedTrader, b Binding) ManagedTrader {
	return boundTrader{ManagedTrader: t, binding: b}
}

func (t boundTrader) ManagedSnapshot(ctx context.Context) (ManagedAccount, error) {
	a, err := t.ManagedTrader.ManagedSnapshot(ctx)
	if err != nil {
		return a, err
	}
	a.AccountKey = t.binding.ID
	return a, nil
}

func (t boundTrader) Events(ctx context.Context, fn func(AccountEvent) error) error {
	return t.ManagedTrader.Events(ctx, func(e AccountEvent) error {
		e.AccountKey = t.binding.ID
		return fn(e)
	})
}
