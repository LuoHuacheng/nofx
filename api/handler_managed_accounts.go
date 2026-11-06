package api

import (
	"context"
	"errors"
	"fmt"
	"nofx/logger"
	"nofx/store"
	"nofx/trader/binance"
	"nofx/trader/execution"
	"nofx/trader/okx"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// managedTraderFactory is a test seam; production builds real venue clients.
type managedTraderFactory func(context.Context, *store.Exchange, execution.Environment) (execution.ManagedTrader, error)

type managedBindingRequest struct {
	ExchangeID              string `json:"exchange_id"`
	VenueAccountID          string `json:"venue_account_id"`
	Environment             string `json:"environment"`
	ConfirmDedicatedAccount bool   `json:"confirm_dedicated_account"`
}

// managedEnvironment maps the saved endpoint to the execution environment, so
// testnet/demo credentials can never reach the live venue.
func managedEnvironment(ex *store.Exchange) (execution.Environment, error) {
	switch ex.ExchangeType {
	case "binance", "okx":
	default:
		return "", errors.New("unattended execution supports Binance and OKX USDT perpetuals only")
	}
	if ex.Testnet {
		return execution.EnvironmentSandbox, nil
	}
	return execution.EnvironmentLive, nil
}

func (s *Server) managedExchange(user, id string) (*store.Exchange, execution.Environment, error) {
	ex, e := s.store.Exchange().GetByID(user, id)
	if e != nil || !ex.Enabled {
		return nil, "", errors.New("select an enabled NOFX trading account owned by the current user")
	}
	env, e := managedEnvironment(ex)
	if e != nil {
		return nil, "", e
	}
	return ex, env, nil
}

func (s *Server) managedAdapter(ctx context.Context, ex *store.Exchange, env execution.Environment) (execution.ManagedTrader, error) {
	if s.managedTraderFactory != nil {
		return s.managedTraderFactory(ctx, ex, env)
	}
	switch ex.ExchangeType {
	case "binance":
		if ex.APIKey == "" || ex.SecretKey == "" {
			return nil, errors.New("connect a Binance futures API key and secret in NOFX first")
		}
		return binance.NewManagedAccountTrader(ctx, string(ex.APIKey), string(ex.SecretKey), ex.UserID, env)
	case "okx":
		if ex.APIKey == "" || ex.SecretKey == "" || ex.Passphrase == "" {
			return nil, errors.New("connect the OKX API key, secret and passphrase in NOFX first")
		}
		return okx.NewManagedAccountTrader(ctx, string(ex.APIKey), string(ex.SecretKey), string(ex.Passphrase), env)
	}
	return nil, errors.New("unattended execution supports Binance and OKX USDT perpetuals only")
}

// managedTrader returns the venue client tagged with the durable binding ID.
// Unverified or environment-mismatched bindings fail closed.
func (s *Server) managedTrader(ctx context.Context, user, exchangeID string) (execution.ManagedTrader, execution.Binding, error) {
	ex, env, e := s.managedExchange(user, exchangeID)
	if e != nil {
		return nil, execution.Binding{}, e
	}
	b, e := execution.ResolveBinding(ctx, s.store.GormDB(), user, exchangeID)
	if e != nil {
		return nil, execution.Binding{}, e
	}
	if !b.Enabled || b.VerifiedAt.IsZero() {
		return nil, execution.Binding{}, errors.New("this physical account is not verified; unattended execution stays disabled")
	}
	if b.Venue != ex.ExchangeType || b.Environment != env {
		return nil, execution.Binding{}, errors.New("the managed binding does not match the saved exchange configuration")
	}
	t, e := s.managedAdapter(ctx, ex, env)
	if e != nil {
		return nil, execution.Binding{}, e
	}
	return execution.WithBinding(t, b), b, nil
}

// refreshManagedBinding voids the physical-identity proof when the saved
// credentials or endpoint change, so a rotated key or swapped account never
// inherits it. The row is kept so the same account can be re-verified.
func (s *Server) refreshManagedBinding(ex *store.Exchange, apiKey, secretKey, passphrase string, testnet bool) {
	if apiKey == strings.TrimSpace(string(ex.APIKey)) &&
		secretKey == strings.TrimSpace(string(ex.SecretKey)) &&
		passphrase == strings.TrimSpace(string(ex.Passphrase)) &&
		testnet == ex.Testnet {
		return
	}
	err := s.store.GormDB().Model(&execution.Binding{}).
		Where("user_id = ? AND exchange_id = ?", ex.UserID, ex.ID).
		Updates(map[string]any{"enabled": false, "verified_at": time.Time{}}).Error
	if err != nil {
		logger.Infof("⚠️ Failed to invalidate managed account binding %s: %v", ex.ID, err)
	}
}

// deleteManagedBinding drops the binding together with the credential
// configuration, so a re-created configuration never inherits a stale proof.
func (s *Server) deleteManagedBinding(userID, exchangeID string) {
	err := s.store.GormDB().Where("user_id = ? AND exchange_id = ?", userID, exchangeID).Delete(&execution.Binding{}).Error
	if err != nil {
		logger.Infof("⚠️ Failed to delete managed account binding %s: %v", exchangeID, err)
	}
}

func (s *Server) handleManagedAccountBinding(c *gin.Context) {
	user := c.GetString("user_id")
	var in managedBindingRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		planningError(c, errors.New("invalid managed account binding request"))
		return
	}
	// Migrate directly: binding must not depend on the planning controller.
	if e := s.store.GormDB().AutoMigrate(&execution.Binding{}); e != nil {
		c.JSON(503, gin.H{"error": "managed account binding unavailable"})
		return
	}
	ex, env, e := s.managedExchange(user, in.ExchangeID)
	if e != nil {
		planningError(c, e)
		return
	}
	if execution.Environment(in.Environment) != env {
		planningError(c, errors.New("the requested environment does not match the saved exchange configuration"))
		return
	}
	id := strings.TrimSpace(in.VenueAccountID)
	if id == "" {
		planningError(c, errors.New("a venue account ID is required; credentials are never used as a physical identity"))
		return
	}
	t, e := s.managedAdapter(c.Request.Context(), ex, env)
	if e != nil {
		planningError(c, e)
		return
	}
	b := execution.Binding{UserID: user, ExchangeID: ex.ID, Venue: ex.ExchangeType, Environment: env, VenueAccountID: id}
	if reader, ok := t.(execution.AccountIdentity); ok {
		actual, e := reader.VenueAccountID(c.Request.Context())
		if e != nil {
			planningError(c, fmt.Errorf("could not verify the venue account identity: %w", e))
			return
		}
		if actual != id {
			planningError(c, errors.New("the venue account ID does not match the configured API credentials"))
			return
		}
		b.VerifiedAt = time.Now().UTC()
		b.Enabled = true
	} else if !in.ConfirmDedicatedAccount {
		planningError(c, errors.New("this venue cannot prove an immutable account ID; confirm the account is dedicated before binding"))
		return
	}
	if b.Enabled {
		// ruling: same account lock as AutoTrader.Run, so a start cannot pass its
		// own check between this assertion and the binding write.
		unlock, e := s.lockPlanningAccount(user, ex.ID)
		if e != nil {
			planningError(c, e)
			return
		}
		defer unlock()
		if e := s.assertLegacyStoppedForManagedBinding(user, ex.ID); e != nil {
			planningError(c, e)
			return
		}
	}
	b, e = execution.BindManagedAccount(c.Request.Context(), s.store.GormDB(), b)
	if e != nil {
		planningError(c, e)
		return
	}
	c.JSON(200, b)
}

// assertLegacyStoppedForManagedBinding refuses to enable the binding while a
// legacy autotrader is still marked as running on this account.
// ruling: enabling first would let the legacy autotrader and managed execution
// trade one physical account, so the legacy stop is a precondition of enabling,
// never a follow-up; stopping a live strategy stays an explicit user action.
func (s *Server) assertLegacyStoppedForManagedBinding(user, exchangeID string) error {
	rows, e := s.store.Trader().ListByExchangeID(user, exchangeID)
	if e != nil {
		return errors.New("cannot verify whether automatic trading is still running on this account")
	}
	for _, row := range rows {
		active := row.IsRunning
		if s.traderManager != nil {
			if runtime, e := s.traderManager.GetTrader(row.ID); e == nil {
				if running, ok := runtime.GetStatus()["is_running"].(bool); ok {
					active = active || running
				}
			}
		}
		if active {
			return errors.New("stop automatic trading on this account before enabling unattended execution")
		}
	}
	return nil
}

func (s *Server) registerManagedAccounts(protected *gin.RouterGroup) {
	protected.POST("/managed-accounts/bindings", s.handleManagedAccountBinding)
}
