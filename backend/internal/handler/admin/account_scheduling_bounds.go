package admin

import "github.com/Wei-Shaw/sub2api/internal/service"

// Unconfigured / imported accounts default to the bottom of the 1..30 band
// (same conservative semantics as migration 231 / pre-band default 50).
const defaultImportedAccountPriority = service.AccountPriorityMax

func createAccountPriority(priority *int) int {
	if priority == nil {
		return service.AccountPriorityMax
	}
	return *priority
}

func validateAccountSchedulingFields(priority *int, loadFactor *int) error {
	if priority != nil {
		if err := service.ValidateAccountPriority(*priority); err != nil {
			return err
		}
	}
	if loadFactor != nil {
		if err := service.ValidateAccountLoadFactor(*loadFactor, true); err != nil {
			return err
		}
	}
	return nil
}

// validateCreateAccountSchedulingFields validates non-pointer create payloads
// where an omitted priority binds to zero. Zero means "unconfigured" and is
// mapped to the band default by createAccountPriority, so it must not trip
// the 1..30 range check.
func validateCreateAccountSchedulingFields(priority int, loadFactor *int) error {
	if priority != 0 {
		if err := service.ValidateAccountPriority(priority); err != nil {
			return err
		}
	}
	if loadFactor != nil {
		if err := service.ValidateAccountLoadFactor(*loadFactor, true); err != nil {
			return err
		}
	}
	return nil
}
