package registry

import (
	"fmt"
	"strconv"
	"strings"
)

type SemVer struct {
	Major int
	Minor int
	Patch int
}

func ParseSemVer(version string) (*SemVer, error) {
	version = strings.TrimSpace(version)
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid semantic version: %s (expected major.minor.patch)", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return nil, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return nil, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil || patch < 0 {
		return nil, fmt.Errorf("invalid patch version: %s", parts[2])
	}

	return &SemVer{Major: major, Minor: minor, Patch: patch}, nil
}

func (v *SemVer) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v *SemVer) Compare(other *SemVer) int {
	if v.Major != other.Major {
		return v.Major - other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor - other.Minor
	}
	return v.Patch - other.Patch
}

func (v *SemVer) GreaterThan(other *SemVer) bool {
	return v.Compare(other) > 0
}

func (v *SemVer) LessThan(other *SemVer) bool {
	return v.Compare(other) < 0
}

func (v *SemVer) Equal(other *SemVer) bool {
	return v.Compare(other) == 0
}

type SemVerList []*SemVer

func (l SemVerList) Len() int           { return len(l) }
func (l SemVerList) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l SemVerList) Less(i, j int) bool { return l[i].GreaterThan(l[j]) }
