// SPDX-License-Identifier: GPL-3.0-or-later
package app

import "time"

func updateBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 5 {
		return 2 * time.Hour
	}
	delay := 5 * time.Minute * time.Duration(1<<(failures-1))
	if delay > 2*time.Hour {
		return 2 * time.Hour
	}
	return delay
}

func nextProfileUpdate(p Profile) time.Time {
	if p.URL == "" || p.UpdateHours <= 0 {
		return time.Time{}
	}
	if p.UpdateError != "" {
		if retry, err := time.Parse(time.RFC3339, p.RetryAt); err == nil {
			return retry
		}
	}
	if last, err := time.Parse(time.RFC3339, p.UpdatedAt); err == nil {
		return last.Add(time.Duration(p.UpdateHours) * time.Hour)
	}
	return time.Time{}
}
