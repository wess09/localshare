package service

import (
	"context"
	"fmt"
	"log/slog"
)

func Go(ctx context.Context, log *slog.Logger, name string, fn func()) {
	go func() {
		defer recoverPanic(log, name)
		select {
		case <-ctx.Done():
			return
		default:
			fn()
		}
	}()
}

func RecoverError(log *slog.Logger, name string, err *error) {
	if value := recover(); value != nil {
		if log != nil {
			log.Error("goroutine panic recovered", "name", name, "panic", value)
		}
		*err = fmt.Errorf("%s panic: %v", name, value)
	}
}

func recoverPanic(log *slog.Logger, name string) {
	if value := recover(); value != nil && log != nil {
		log.Error("goroutine panic recovered", "name", name, "panic", value)
	}
}
