//go:build !linux

package installer

import (
	"context"
	"errors"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
	RunInput(context.Context, []byte, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("Linux required")
}
func (ExecRunner) RunInput(context.Context, []byte, string, ...string) ([]byte, error) {
	return nil, errors.New("Linux required")
}

type FirewallSnapshot struct {
	Active          bool
	DefaultIncoming string
}
type Firewall struct{ Runner Runner }

func (Firewall) Preflight(context.Context) (FirewallSnapshot, error) {
	return FirewallSnapshot{}, errors.New("Linux required")
}
func (Firewall) Plan(context.Context, int, int) ([]string, error) {
	return nil, errors.New("Linux required")
}
func (Firewall) Apply(context.Context, []string, int, FirewallSnapshot) error {
	return errors.New("Linux required")
}
func (Firewall) RemoveRules(context.Context, []string, FirewallSnapshot) error {
	return errors.New("Linux required")
}
