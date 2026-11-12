//go:build !darwin && !linux && !windows

package loginsvc

import "context"

func (s Service) install(context.Context) (Status, error) { return Status{}, ErrUnsupported }
func (s Service) uninstall(context.Context) error         { return ErrUnsupported }
func (s Service) check(context.Context) Status            { return Status{} }
