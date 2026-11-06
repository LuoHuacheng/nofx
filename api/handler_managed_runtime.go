package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"nofx/mcp"
	"nofx/planning/model"
	"nofx/trader/execution"
	"nofx/trading/automation"
	"nofx/trading/risk"
)

// StartManagedTrading starts the unattended supervisor on the same controller
// the API uses. It is opt-in: nothing trades until a policy is enabled and
// started, and an unverified binding keeps every account inert.
func (s *Server) StartManagedTrading(ctx context.Context) error {
	p, e := s.planningService()
	if e != nil {
		return e
	}
	risks, e := risk.NewStore(s.store.GormDB())
	if e != nil {
		return e
	}
	p.live.Risk = risks
	factory := s.managedTrader
	rt, e := automation.NewRuntime(s.store.GormDB(), p.live, risks, func(ctx context.Context, user, exchangeID string) (execution.ManagedTrader, execution.Binding, error) {
		return factory(ctx, user, exchangeID)
	}, &signalRouter{server: s}, func() time.Time { return time.Now().UTC() })
	if e != nil {
		return e
	}
	s.planningMu.Lock()
	s.managedRuntime = rt
	s.planningMu.Unlock()
	return rt.Start(ctx)
}

// managedRuntimeHandle returns the supervisor when StartManagedTrading ran.
func (s *Server) managedRuntimeHandle() *automation.Runtime {
	s.planningMu.RLock()
	defer s.planningMu.RUnlock()
	return s.managedRuntime
}

// signalRouter builds a model source from the account owner's own enabled AI
// model. Credentials stay in NOFX's client and never enter model messages.
type signalRouter struct {
	server  *Server
	mu      sync.Mutex
	sources map[string]automation.IdeaSource
}

func (r *signalRouter) Next(ctx context.Context, policy risk.Policy) (automation.Signal, error) {
	source, err := r.sourceFor(policy.UserID)
	if err != nil {
		return automation.Signal{}, err
	}
	return source.Next(ctx, policy)
}

func (r *signalRouter) sourceFor(user string) (automation.IdeaSource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if source, ok := r.sources[user]; ok {
		return source, nil
	}
	source, err := r.server.managedSignalSource(user)
	if err != nil {
		return nil, err
	}
	if r.sources == nil {
		r.sources = map[string]automation.IdeaSource{}
	}
	r.sources[user] = source
	return source, nil
}

// managedSignalSource builds the model-backed source for one user from that
// user's first enabled AI model.
func (s *Server) managedSignalSource(user string) (automation.IdeaSource, error) {
	models, e := s.store.AIModel().List(user)
	if e != nil {
		return nil, e
	}
	p, e := s.planningService()
	if e != nil {
		return nil, e
	}
	for _, ai := range models {
		if !ai.Enabled || ai.APIKey == "" {
			continue
		}
		client := mcp.NewAIClientByProvider(ai.Provider)
		if client == nil {
			continue
		}
		url := ai.CustomAPIURL
		if ai.Provider == "claw402" {
			url = ""
		}
		client.SetAPIKey(string(ai.APIKey), url, ai.CustomModelName)
		client.SetTimeout(90 * time.Second)
		planner := &model.NOFXAdapter{Client: client, TextTools: model.RequiresTextTools(ai.Provider, ai.CustomModelName)}
		return automation.NewModelSource(planner, p.source, nil), nil
	}
	return nil, errors.New("no enabled AI model is configured for unattended signals")
}
