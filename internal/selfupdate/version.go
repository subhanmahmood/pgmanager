package selfupdate

import (
	"fmt"
	"strconv"
	"strings"
)

// version is a minimal semver representation. Build metadata (everything after
// a '+') is parsed and discarded; prerelease identifiers (after a '-') are kept
// because they affect ordering.
type version struct {
	major, minor, patch int
	pre                 string // prerelease identifiers, '.'-separated, no leading '-'
}

// parseVersion accepts tags with or without a leading 'v' (e.g. "v0.2.0",
// "0.2.0", "v1.0.0-rc.1").
func parseVersion(s string) (version, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return version{}, fmt.Errorf("empty version %q", orig)
	}
	if i := strings.IndexByte(s, '+'); i >= 0 { // drop build metadata
		s = s[:i]
	}
	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return version{}, fmt.Errorf("invalid version %q", orig)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, fmt.Errorf("invalid version %q", orig)
		}
		nums[i] = n
	}
	return version{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, nil
}

// compare returns -1, 0, or 1 if a is less than, equal to, or greater than b.
func (a version) compare(b version) int {
	if c := cmpInt(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpInt(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpInt(a.patch, b.patch); c != 0 {
		return c
	}
	return comparePre(a.pre, b.pre)
}

// comparePre implements semver precedence for prerelease identifiers. A version
// without a prerelease outranks one with a prerelease (1.0.0 > 1.0.0-rc.1).
func comparePre(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := comparePreIdent(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(as), len(bs))
}

func comparePreIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return cmpInt(an, bn)
	case aerr == nil: // numeric identifiers rank below alphanumeric ones
		return -1
	case berr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
