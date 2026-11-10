package modelfamily

import "strconv"

func strconvFormat(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
