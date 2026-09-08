package journal

import (
	"regexp"
	"strings"
)

// secretKeyWords is the closed list of KEY=VALUE identifier spellings rule 1
// treats as secret, matched as whole underscore-delimited segments of KEY
// (see isSecretKey) rather than as a substring, so that PASSPORT_ID is not
// caught by "pass".
var secretKeyWords = map[string]bool{
	"pass":       true,
	"passwd":     true,
	"password":   true,
	"pwd":        true,
	"passphrase": true,
	"token":      true,
	"secret":     true,
	"apikey":     true,
	"auth":       true,
	"credential": true,
	"session":    true,
}

// reKeyValue matches a shell KEY=VALUE assignment. Whether KEY is one of the
// secret spellings is decided separately, by isSecretKey: Go's regexp
// package is RE2, which has no lookaround, so the word-boundary rule the
// closed list needs (KEY must BE one of the spellings, not merely contain
// one) cannot be expressed inside the pattern itself.
var reKeyValue = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_-]*=\S+`)

// reFlagValueEq and reFlagValueSpace match rule 2's six long flags, in the
// "--flag=value" and "--flag value" forms respectively. The flag spelling
// is captured and kept verbatim; only the value is replaced.
var (
	reFlagValueEq    = regexp.MustCompile(`(?i)(--(?:password|token|api-key|secret|auth|credential))=\S+`)
	reFlagValueSpace = regexp.MustCompile(`(?i)(--(?:password|token|api-key|secret|auth|credential))(\s+)\S+`)
)

// reMysqlPAttached and reMysqlPSpace match rule 3's bare -p password flag
// after mysql, psql or mysqldump, attached (-pVALUE) and space-separated
// (-p VALUE) respectively. The intervening group allows up to four other
// flags or values (-u root, -h db.internal, and the like) between the
// command word and -p, which is what the standard "mysql -u root
// -phunter2" invocation needs: -p need not be the first flag. It is
// bounded to four tokens, and not left unbounded, so a -p flag belonging to
// some unrelated later command on the same line is not swept in by
// accident. reCurlUserPass matches curl's -u user:pass form, keeping the
// username and redacting only the password; it is anchored to a preceding
// \bcurl\b so an unrelated -u flag, such as docker run's -u uid:gid, is
// left alone. Neither anchor can be expressed as a zero-width assertion:
// RE2 has no lookaround, so both patterns capture the command word and
// everything between it and the flag, and the replacement re-emits that
// captured text unchanged.
var (
	reMysqlPAttached = regexp.MustCompile(`(?i)\b(mysql|psql|mysqldump)\b((?:\s+\S+){0,4}?)(\s+)-p(\S+)`)
	reMysqlPSpace    = regexp.MustCompile(`(?i)\b(mysql|psql|mysqldump)\b((?:\s+\S+){0,4}?)(\s+)-p(\s+)\S+`)
	reCurlUserPass   = regexp.MustCompile(`(?i)(\bcurl\b[^\n]*?)\s+-u(\s+)([^:\s]+):\S+`)
)

// reAuthHeader matches rule 4's Authorization header, case insensitive on
// both the header name and the Bearer/Basic scheme. The output always uses
// the canonical "Authorization: Bearer" or "Authorization: Basic" spelling,
// regardless of how the input was cased.
var reAuthHeader = regexp.MustCompile(`(?i)authorization\s*:\s*(bearer|basic)\s+[^\s'"]+`)

// rePEMBlock matches rule 5: everything from a -----BEGIN line through the
// end of its matching -----END line, inclusive.
var rePEMBlock = regexp.MustCompile(`(?s)-----BEGIN.*?-----END[^\n]*`)

// Redact removes the secret material a learner may have typed into their
// shell. It returns the cleaned command and whether anything changed.
//
// The rules are a closed list, documented in doc.go, and deliberately not an
// entropy or length heuristic: files-04 asks the learner to compare sha256
// sums, so a learner types a 64 character hex string, and an entropy rule
// would eat exactly the input a maintainer needs in order to understand why
// important-intact failed. A closed list that misses something is a known
// gap; a heuristic that eats the evidence is a silent one.
func Redact(raw string) (string, bool) {
	out := raw
	out = redactKeyValue(out)
	out = redactLongFlags(out)
	out = redactDBFlags(out)
	out = redactAuthHeader(out)
	out = redactPEMBlocks(out)
	return out, out != raw
}

// redactKeyValue applies rule 1.
func redactKeyValue(s string) string {
	return reKeyValue.ReplaceAllStringFunc(s, func(m string) string {
		eq := strings.IndexByte(m, '=')
		if eq < 0 {
			return m
		}
		key := m[:eq]
		if !isSecretKey(key) {
			return m
		}
		return key + "=[redacted]"
	})
}

// isSecretKey reports whether key, once its spelling is normalized and split
// on '_' and '-', has a segment that IS one of secretKeyWords, or has the
// two consecutive segments "api" and "key" (the split form of api_key and
// api-key). A segment merely containing one of the words, such as
// "passport" containing "pass", does not count.
func isSecretKey(key string) bool {
	norm := strings.ToLower(strings.NewReplacer("-", "_").Replace(key))
	segs := strings.Split(norm, "_")
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		if secretKeyWords[seg] {
			return true
		}
		if seg == "api" && i+1 < len(segs) && segs[i+1] == "key" {
			return true
		}
	}
	// A key with no underscore or hyphen to split on can still mash a
	// secret word onto a prefix, the way PGPASSWORD does. "password" is
	// long and specific enough that a suffix match on it does not also
	// catch an unrelated word ending in a shorter fragment: PASSPORT_ID
	// ends in "id" as its own segment, already handled and rejected above,
	// and nothing in this codebase's vocabulary ends in the full word
	// "password" without meaning one.
	if norm != "password" && strings.HasSuffix(norm, "password") {
		return true
	}
	return false
}

// redactLongFlags applies rule 2.
func redactLongFlags(s string) string {
	s = reFlagValueEq.ReplaceAllString(s, "$1=[redacted]")
	s = reFlagValueSpace.ReplaceAllString(s, "$1$2[redacted]")
	return s
}

// redactDBFlags applies rule 3.
func redactDBFlags(s string) string {
	s = reMysqlPAttached.ReplaceAllString(s, "$1$2$3-p [redacted]")
	s = reMysqlPSpace.ReplaceAllString(s, "$1$2$3-p$4[redacted]")
	s = reCurlUserPass.ReplaceAllString(s, "$1 -u$2$3:[redacted]")
	return s
}

// redactAuthHeader applies rule 4, normalizing the header name and scheme
// to their canonical casing in the output.
func redactAuthHeader(s string) string {
	return reAuthHeader.ReplaceAllStringFunc(s, func(m string) string {
		sub := reAuthHeader.FindStringSubmatch(m)
		scheme := "Bearer"
		if len(sub) > 1 && strings.EqualFold(sub[1], "basic") {
			scheme = "Basic"
		}
		return "Authorization: " + scheme + " [redacted]"
	})
}

// redactPEMBlocks applies rule 5.
func redactPEMBlocks(s string) string {
	return rePEMBlock.ReplaceAllString(s, "[redacted key]")
}
