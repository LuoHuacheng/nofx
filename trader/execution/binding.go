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
	UserID         string      `gorm:"column:user_id;not null;uniqueIndex:managed_account_config,priority:1" json:"user_id"`
	ExchangeID     string      `gorm:"column:exchange_id;not null;uniqueIndex:managed_account_config,priority:2" json:"exchange_id"`
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

// BindManagedAccount creates, rotates or re-verifies the binding of one
// credential configuration. One physical account keeps exactly one binding and
// one credential configuration exactly one physical account. Only a verified
// identity may claim an unverified record, and moving a configuration onto a
// different physical account mints a new binding ID so runs and leases never
// carry over to another account.
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
	verified := !b.VerifiedAt.IsZero()
	var out Binding
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity, config Binding
		identityFound, err := firstBinding(tx, &identity, "venue = ? AND environment = ? AND venue_account_id = ?", b.Venue, b.Environment, b.VenueAccountID)
		if err != nil {
			return err
		}
		configFound, err := firstBinding(tx, &config, "user_id = ? AND exchange_id = ?", b.UserID, b.ExchangeID)
		if err != nil {
			return err
		}
		switch {
		case identityFound && configFound && identity.ID == config.ID:
			// Rotation of the same credentials for the same physical account.
			updates := map[string]any{"verified_at": b.VerifiedAt, "enabled": b.Enabled}
			if err := tx.Model(&identity).Updates(updates).Error; err != nil {
				return err
			}
			identity.VerifiedAt, identity.Enabled = b.VerifiedAt, b.Enabled
			out = identity
			return nil
		case identityFound && !configFound:
			// Only proof can claim a global identity, so an unverified record
			// (an initialization stub or a squatter) cannot lock it away.
			if !verified || !identity.VerifiedAt.IsZero() {
				return errors.New("this physical account is already bound to another NOFX account")
			}
			updates := map[string]any{
				"user_id":     b.UserID,
				"exchange_id": b.ExchangeID,
				"verified_at": b.VerifiedAt,
				"enabled":     b.Enabled,
			}
			if err := tx.Model(&identity).Updates(updates).Error; err != nil {
				return err
			}
			identity.UserID, identity.ExchangeID = b.UserID, b.ExchangeID
			identity.VerifiedAt, identity.Enabled = b.VerifiedAt, b.Enabled
			out = identity
			return nil
		case identityFound && configFound:
			// Different rows: the verified proof releases the stale unverified
			// identity and the configuration is re-pointed below.
			if !verified || !identity.VerifiedAt.IsZero() {
				return errors.New("this physical account is already bound to another NOFX account")
			}
			if err := tx.Delete(&Binding{}, "id = ?", identity.ID).Error; err != nil {
				return err
			}
		}
		if configFound {
			if config.Venue != b.Venue {
				return errors.New("this credential configuration is bound to another venue")
			}
			if config.VenueAccountID != b.VenueAccountID {
				// A different physical account must not inherit the durable key.
				if !verified || !config.VerifiedAt.IsZero() {
					return errors.New("this credential configuration is already bound to another physical account")
				}
				if err := tx.Delete(&Binding{}, "id = ?", config.ID).Error; err != nil {
					return err
				}
			} else {
				// Same account, moved endpoint (testnet flip): keep the key.
				updates := map[string]any{"environment": b.Environment, "verified_at": b.VerifiedAt, "enabled": b.Enabled}
				if err := tx.Model(&config).Updates(updates).Error; err != nil {
					return err
				}
				config.Environment, config.VerifiedAt, config.Enabled = b.Environment, b.VerifiedAt, b.Enabled
				out = config
				return nil
			}
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

func firstBinding(tx *gorm.DB, dst *Binding, query string, args ...any) (bool, error) {
	err := tx.Where(query, args...).First(dst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
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
