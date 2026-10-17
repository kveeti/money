package state

import (
	"context"
	"money/backend/auth"
	"money/backend/config"
	"money/backend/data"
	"money/backend/enablebanking"
	"sync"
)

type State struct {
	Context             context.Context
	Data                *data.Data
	Config              *config.Config
	OIDC                *auth.OIDC
	TokenCipher         *auth.TokenCipher
	EnableBankingClient *enablebanking.Client
	EnableBankingSyncer *enablebanking.Syncer
	earlyRefreshMu      sync.Mutex
	earlyRefreshes      map[string]struct{}
}

func NewState(ctx context.Context, d *data.Data, c *config.Config, o *auth.OIDC) *State {
	return &State{
		Context:        ctx,
		Data:           d,
		Config:         c,
		OIDC:           o,
		TokenCipher:    auth.NewTokenCipher(c.SessionEncryptionKey),
		earlyRefreshes: make(map[string]struct{}),
	}
}

func (s *State) BeginEarlyRefresh(sessionID string) bool {
	s.earlyRefreshMu.Lock()
	defer s.earlyRefreshMu.Unlock()
	if _, isRefreshing := s.earlyRefreshes[sessionID]; isRefreshing || len(s.earlyRefreshes) >= 5 {
		return false
	}
	s.earlyRefreshes[sessionID] = struct{}{}
	return true
}

func (s *State) EndEarlyRefresh(sessionID string) {
	s.earlyRefreshMu.Lock()
	defer s.earlyRefreshMu.Unlock()
	delete(s.earlyRefreshes, sessionID)
}
