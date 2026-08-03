package driver

import (
	"fmt"
	"sync"
)

var (
	mu      sync.RWMutex
	drivers = make(map[string]Driver)
)

func Register(name string, d Driver) {
	mu.Lock()
	defer mu.Unlock()
	drivers[name] = d
}

// retiredRunners maps a runner dropped by ADR-026 to the reason it went and
// the supported runner closest to it. A retired type fails compilation with
// this guidance rather than the generic unknown-driver message, so an operator
// on an old pod file learns what happened instead of guessing at a typo.
var retiredRunners = map[string]string{
	"nanoclaw":  "no independent distribution channel to corroborate adoption, and the steepest adoption decay in the runner set; closest supported runner: nanobot",
	"microclaw": "no material adoption growth; closest supported runner: picoclaw",
	"nullclaw":  "upstream is no longer viable (no release in over 60 days); closest supported runner: picoclaw",
}

func Lookup(name string) (Driver, error) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		if reason, retired := retiredRunners[name]; retired {
			return nil, fmt.Errorf("CLAW_TYPE %q was retired in ADR-026: %s", name, reason)
		}
		return nil, fmt.Errorf("unknown CLAW_TYPE %q: no registered driver", name)
	}
	return d, nil
}

func Registered() map[string]Driver {
	mu.RLock()
	defer mu.RUnlock()

	out := make(map[string]Driver, len(drivers))
	for name, d := range drivers {
		out[name] = d
	}
	return out
}
