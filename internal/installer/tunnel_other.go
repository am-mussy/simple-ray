//go:build !linux

package installer

import (
	"context"
	"errors"

	"github.com/mussy/simple-ray/internal/domain"
)

func verifyRealityTunnel(context.Context, string, domain.ClientLink) error {
	return errors.New("Linux required")
}

func ProbeClientLink(context.Context, string, domain.ClientLink) (string, error) {
	return "", errors.New("Linux required")
}
