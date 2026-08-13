//go:build !linux

package installer

import (
	"context"
	"errors"

	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/state"
)

type Request struct {
	User, ServerName, PublicAddress string
	ListenPort, SSHPort             int
	RealitySNI, RealityTarget       string
}
type Result struct {
	State    domain.State `json:"state"`
	User     domain.User  `json:"user"`
	Existing bool         `json:"existing"`
}
type Manager struct{}

func NewManager(*state.Store) *Manager { return &Manager{} }
func (*Manager) Install(context.Context, Request) (Result, error) {
	return Result{}, errors.New("installation requires Linux")
}
func (*Manager) Uninstall(context.Context, domain.State, bool, bool) error {
	return errors.New("uninstall requires Linux")
}
