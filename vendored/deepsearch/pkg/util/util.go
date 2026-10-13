package util

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
)

// version is injected at build time with
// -ldflags "-X deepsearch/pkg/util.version=v1.2.3" and wins over the VCS
// revision.
var version string

type SetupHandler func(context.Context) error

type SetupHandlers []SetupHandler

func (h SetupHandlers) Setup(ctx context.Context) error {
	for idx, setup := range h {
		if e := setup(ctx); e != nil {
			return fmt.Errorf("failed to setup #%d: %w", idx, e)
		}
	}

	return nil
}

func Stacks(maxDepth, skip int) []string {
	stacks := make([]string, 0, maxDepth*2)
	for i := skip; i < maxDepth; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}

		stacks = append(stacks, file+":"+strconv.Itoa(line))

		if fn := runtime.FuncForPC(pc); fn != nil {
			stacks = append(stacks, fn.Name())
			continue
		}

		stacks = append(stacks, "(no function)")
	}
	return stacks
}

// BuildVersion reports the version of this build: the injected version, the
// short VCS revision, or "dev" for a plain local build.
func BuildVersion() string {
	if version != "" {
		return version
	}

	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}

	for _, s := range buildInfo.Settings {
		if s.Key != "vcs.revision" {
			continue
		}

		if l := len(s.Value); l < 1 {
			return "dev"
		} else if l > 7 {
			return s.Value[:7]
		}

		return s.Value
	}

	return "dev"
}
