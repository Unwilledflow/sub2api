package service

import (
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AccountPriorityMin = 1
	AccountPriorityMax = 30

	AccountLoadFactorMin = 1
	AccountLoadFactorMax = 1000
)

// ValidateAccountPriority validates the persistent account scheduling priority.
func ValidateAccountPriority(value int) error {
	if value < AccountPriorityMin || value > AccountPriorityMax {
		return infraerrors.BadRequest(
			"ACCOUNT_PRIORITY_OUT_OF_RANGE",
			fmt.Sprintf("priority must be between %d and %d", AccountPriorityMin, AccountPriorityMax),
		)
	}
	return nil
}

// ValidateAccountLoadFactor validates a persistent account load factor.
// A zero value is accepted only for update-style requests, where it clears the override.
func ValidateAccountLoadFactor(value int, allowClear bool) error {
	min := AccountLoadFactorMin
	if allowClear {
		min = 0
	}
	if value < min || value > AccountLoadFactorMax {
		if allowClear {
			return infraerrors.BadRequest(
				"ACCOUNT_LOAD_FACTOR_OUT_OF_RANGE",
				fmt.Sprintf("load_factor must be 0 or between %d and %d", AccountLoadFactorMin, AccountLoadFactorMax),
			)
		}
		return infraerrors.BadRequest(
			"ACCOUNT_LOAD_FACTOR_OUT_OF_RANGE",
			fmt.Sprintf("load_factor must be between %d and %d", AccountLoadFactorMin, AccountLoadFactorMax),
		)
	}
	return nil
}

// ClampAccountPriority bounds legacy or imported values before they enter routing.
func ClampAccountPriority(value int) int {
	if value < AccountPriorityMin {
		return AccountPriorityMin
	}
	if value > AccountPriorityMax {
		return AccountPriorityMax
	}
	return value
}

// ClampAccountLoadFactor bounds legacy or imported positive values. Zero remains the
// update sentinel for clearing an account override.
func ClampAccountLoadFactor(value int) int {
	if value <= 0 {
		return 0
	}
	if value > AccountLoadFactorMax {
		return AccountLoadFactorMax
	}
	return value
}
