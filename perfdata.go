package main

import (
	"math"
	"strconv"
	"strings"
)

var posInf = math.Inf(1)
var negInf = math.Inf(-1)

type optionalThreshold struct {
	isSet, inverted bool
	start, end      float64
}

func (self *optionalThreshold) String() string {
	if self.isSet {
		builder := strings.Builder{}

		if self.inverted {
			builder.WriteByte('@')
		}

		if self.start == 0 {
			if self.end == posInf {
				builder.WriteString("0:")
			} else {
				builder.WriteString(perfFloat(self.end))
			}
		} else {
			if self.start == negInf {
				builder.WriteByte('~')
			} else {
				builder.WriteString(perfFloat(self.start))
			}

			builder.WriteByte(':')

			if self.end != posInf {
				builder.WriteString(perfFloat(self.end))
			}
		}

		return builder.String()
	}

	return ""
}

type optionalNumber struct {
	isSet bool
	value float64
}

func (self *optionalNumber) String() string {
	if self.isSet {
		return perfFloat(self.value)
	}

	return ""
}

type perfdata struct {
	label, uom string
	value      float64
	warn, crit optionalThreshold
	min, max   optionalNumber
}

func (self *perfdata) String() string {
	builder := strings.Builder{}

	builder.WriteByte('\'')
	builder.WriteString(self.label)
	builder.WriteByte('\'')

	builder.WriteByte('=')

	builder.WriteString(perfFloat(self.value))
	builder.WriteString(self.uom)

	return strings.TrimRight(
		strings.Join(
			[]string{builder.String(), self.warn.String(), self.crit.String(), self.min.String(), self.max.String()},
			";",
		),
		";",
	)
}

type perfdataCollection []perfdata

func (self perfdataCollection) String() string {
	if len(self) < 1 {
		return ""
	}

	result := make([]string, len(self))
	for i, perfdat := range self {
		result[i] = perfdat.String()
	}

	return " |" + strings.Join(result, " ")
}

func perfFloat(x float64) string {
	if math.IsNaN(x) {
		x = 0
	} else if math.IsInf(x, 0) {
		if x > 0 {
			x = math.MaxFloat64
		} else {
			x = -math.MaxFloat64
		}
	}

	return strconv.FormatFloat(x, 'f', -1, 64)
}
