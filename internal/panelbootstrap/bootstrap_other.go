//go:build !linux

package panelbootstrap

import (
	"context"
	"errors"
	"io"
)

func Run(context.Context, string, io.Reader) error {
	return errors.New("panel bootstrap is supported only on Linux")
}
