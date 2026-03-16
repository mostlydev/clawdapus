package shared

import "strings"

func IsFiveFieldCron(expr string) bool {
	return len(strings.Fields(strings.TrimSpace(expr))) == 5
}
