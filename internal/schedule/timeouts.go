package schedule

import "time"

// MaxWakeExecTimeout is the longest bounded runner wake that claw-api supports.
// Operator transports that synchronously wait for a final fire result must
// allow this budget plus their own request and process margins.
const LegacyWakeExecTimeout = 2 * time.Minute
const MaxInvocationTimeoutSeconds = 900
const WakeProcessMargin = 20 * time.Second
const MaxWakeExecTimeout = time.Duration(MaxInvocationTimeoutSeconds)*time.Second + WakeProcessMargin

func InvocationWakeTimeout(seconds int) time.Duration {
	return time.Duration(seconds)*time.Second + WakeProcessMargin
}

const ManualFireRequestTimeout = MaxWakeExecTimeout + 5*time.Second
const ManualFireTransportTimeout = ManualFireRequestTimeout + 5*time.Second
