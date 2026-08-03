package schedule

import "time"

// MaxWakeExecTimeout is the longest bounded runner wake that claw-api supports.
// Operator transports that synchronously wait for a final fire result must
// allow this budget plus their own request and process margins.
const MaxWakeExecTimeout = 2 * time.Minute

const ManualFireRequestTimeout = MaxWakeExecTimeout + 5*time.Second
const ManualFireTransportTimeout = ManualFireRequestTimeout + 5*time.Second
