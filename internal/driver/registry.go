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

const retiredRunnerMigrationTarget = "hermes"

// retiredRunners records runner types deliberately removed by ADR-026. Keep
// this compatibility error for one release so old Clawfiles fail with an
// actionable migration instead of looking like typos.
var retiredRunners = map[string]struct{}{
	"nanoclaw":  {},
	"microclaw": {},
	"nullclaw":  {},
}

// RetirementError returns the canonical migration error for a retired runner.
// Other unknown names return nil and keep the generic unknown-driver path.
func RetirementError(name string) error {
	if _, retired := retiredRunners[name]; !retired {
		return nil
	}
	return fmt.Errorf("CLAW_TYPE %q was retired by ADR-026; migrate this Clawfile to CLAW_TYPE %q", name, retiredRunnerMigrationTarget)
}

func Lookup(name string) (Driver, error) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		if err := RetirementError(name); err != nil {
			return nil, err
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
