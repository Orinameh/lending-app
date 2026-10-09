package validator

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	phoneRegex = regexp.MustCompile(`^0[789][01]\d{8}$`) // Nigerian format
)

func ValidateEmail(email string) bool {
	return emailRegex.MatchString(strings.TrimSpace(email))
}

func ValidatePhone(phone string) bool {
	return phoneRegex.MatchString(phone)
}

func ValidateBVN(bvn string) bool {
	if len(bvn) != 11 {
		return false
	}
	for _, ch := range bvn {
		if !unicode.IsDigit(ch) {
			return false
		}
	}
	return true
}

func ValidateNIN(nin string) bool {
	if len(nin) != 11 {
		return false
	}
	for _, ch := range nin {
		if !unicode.IsDigit(ch) {
			return false
		}
	}
	return true
}

func ValidatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	// bcrypt truncates at 72 bytes; reject longer passwords instead of
	// silently colliding on the prefix.
	if len(password) > 72 {
		return errors.New("password must be at most 72 characters")
	}
	var hasUpper, hasLower, hasDigit bool
	for _, ch := range password {
		switch {
		case unicode.IsUpper(ch):
			hasUpper = true
		case unicode.IsLower(ch):
			hasLower = true
		case unicode.IsDigit(ch):
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return errors.New("password must contain uppercase, lowercase and digit")
	}
	return nil
}
