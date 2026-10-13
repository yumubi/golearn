package slogtrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

const (
	finalExitCodeKey = "exit_code"
	finalPanicKey    = "panic"
)

type finalExitCode struct {
	exitCode int
}

func (f *finalExitCode) Any() any {
	return f.exitCode
}

func (f *finalExitCode) String() string {
	return strconv.Itoa(f.exitCode)
}

type finalPanic struct {
	panic any
}

func (f *finalPanic) Any() any {
	return f.panic
}

func (f *finalPanic) String() string {
	return fmt.Sprint(f.panic)
}

func ExitCode(exitCode int) slog.Attr {
	return slog.Any(finalExitCodeKey, &finalExitCode{exitCode})
}

func Panic(panic any) slog.Attr {
	return slog.Any(finalPanicKey, &finalPanic{panic})
}

type FinalHandler struct {
	slog.Handler
}

func NewFinalHandler(h slog.Handler) *FinalHandler {
	return &FinalHandler{Handler: h}
}

func (h *FinalHandler) Handle(ctx context.Context, r slog.Record) error {
	if e := h.Handler.Handle(ctx, r); e != nil {
		return e
	}

	if r.Level != slog.LevelError {
		return nil
	}

	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case finalExitCodeKey:
			if v, ok := attr.Value.Any().(*finalExitCode); ok {
				os.Exit(v.exitCode)
			}

		case finalPanicKey:
			if v, ok := attr.Value.Any().(*finalPanic); ok {
				panic(v.panic)
			}
		}

		return true
	})

	return nil
}
