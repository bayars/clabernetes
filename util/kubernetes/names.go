package kubernetes

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
)

var (
	validDNSLabelConventionPatternsObj    *validDNSLabelConventionPatterns //nolint:gochecknoglobals
	validNSLabelConventionPatternsObjOnce sync.Once                        //nolint:gochecknoglobals
)

const (
	// NameMaxLen is the maximum length for a kubernetes name.
	NameMaxLen = 63
)

type validDNSLabelConventionPatterns struct {
	invalidChars       *regexp.Regexp
	startsWithNonAlpha *regexp.Regexp
	endsWithNonAlpha   *regexp.Regexp
}

func getDNSLabelConventionPatterns() *validDNSLabelConventionPatterns {
	validNSLabelConventionPatternsObjOnce.Do(func() {
		validDNSLabelConventionPatternsObj = &validDNSLabelConventionPatterns{
			invalidChars:       regexp.MustCompile(`[^a-z0-9\-]`),
			startsWithNonAlpha: regexp.MustCompile(`^[^a-z0-9]`),
			endsWithNonAlpha:   regexp.MustCompile(`[^a-z0-9]$`),
		}
	})

	return validDNSLabelConventionPatternsObj
}

// SafeConcatNameKubernetes concats all provided strings into a string joined by "-" - if the final
// string is greater than 63 characters, the string will be shortened, and a hash will be used at
// the end of the string to keep it unique, but safely within allowed lengths.
func SafeConcatNameKubernetes(name ...string) string {
	return SafeConcatNameMax(name, NameMaxLen)
}

// SafeConcatNameMax concats all provided strings into a string joined by "-" - if the final string
// is greater than max characters, the string will be shortened, and a hash will be used at the end
// of the string to keep it unique, but safely within allowed lengths.
func SafeConcatNameMax(name []string, maxLen int) string {
	finalName := strings.Join(name, "-")

	if len(finalName) <= maxLen {
		return finalName
	}

	digest := sha256.Sum256([]byte(finalName))

	return finalName[0:maxLen-8] + "-" + hex.EncodeToString(digest[0:])[0:7]
}

// EnforceDNSLabelConvention attempts to enforce the RFC 1123 label name requirements on s.
// If any characters had to be replaced (not counting case folding), a 7-character SHA256 hash of
// the lowercased original is appended so that distinct names like "safa-test1" and "safa_test1"
// never map to the same DNS / Kubernetes name.
func EnforceDNSLabelConvention(s string) string {
	p := getDNSLabelConventionPatterns()

	lowered := strings.ToLower(s)

	sanitized := p.invalidChars.ReplaceAllString(lowered, "-")
	sanitized = p.startsWithNonAlpha.ReplaceAllString(sanitized, "z")
	sanitized = p.endsWithNonAlpha.ReplaceAllString(sanitized, "z")

	// If lowercasing alone was sufficient (no invalid chars replaced or boundary-fixed),
	// return as-is — no hash needed, name is already unambiguous.
	if lowered == sanitized {
		return sanitized
	}

	// Characters were substituted: append a short hash of the lowercased original so two
	// different names that sanitize to the same base string remain distinguishable.
	digest := sha256.Sum256([]byte(lowered))

	hashSuffix := "-" + hex.EncodeToString(digest[:])[0:7]

	maxBase := NameMaxLen - len(hashSuffix)
	if len(sanitized) > maxBase {
		sanitized = sanitized[0:maxBase]
	}

	return sanitized + hashSuffix
}
