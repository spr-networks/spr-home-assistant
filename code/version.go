package main

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func parseSemver(s string) ([3]int, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

func semverLess(a, b string) bool {
	va, oka := parseSemver(a)
	vb, okb := parseSemver(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return false
}

var versionCheckMtx sync.Mutex
var versionCheckAt time.Time
var versionCheckResult string

// latestAvailableVersion asks the registry (via the SPR API) for published
// release tags and returns the highest plain semver one. Cached for an hour:
// this is a remote registry query, not a local read.
func latestAvailableVersion(current string) string {
	if _, ok := parseSemver(current); !ok {
		// tracking "latest" or a custom channel: no meaningful comparison
		return ""
	}

	versionCheckMtx.Lock()
	defer versionCheckMtx.Unlock()
	if time.Since(versionCheckAt) < time.Hour {
		return versionCheckResult
	}

	var tags []string
	if err := sprRequest("GET", "/releasesAvailable?container=super", nil, &tags); err != nil {
		return versionCheckResult
	}

	best := ""
	for _, tag := range tags {
		if _, ok := parseSemver(tag); !ok {
			continue
		}
		if best == "" || semverLess(best, tag) {
			best = tag
		}
	}
	versionCheckAt = time.Now()
	versionCheckResult = best
	return best
}
