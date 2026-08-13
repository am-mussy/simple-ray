//go:build !linux

package installer

import (
	"context"
	"errors"
)

func verifyRealityTunnel(context.Context, string, string, string, int, string, string, string) error {
	return errors.New("Linux required")
}
